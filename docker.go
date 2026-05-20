package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type DockerWatcher struct {
	cli         *client.Client
	labelPrefix string
	zone        string
	resolver    *Resolver
	log         *slog.Logger
	publishIP   string // "host" or "container"
	hostIP      string // resolved host IP, used when publishIP == "host"
}

func NewDockerWatcher(dockerHost, labelPrefix, zone, publishIP, hostIP string, resolver *Resolver, log *slog.Logger) (*DockerWatcher, error) {
	cli, err := client.NewClientWithOpts(
		client.WithHost(dockerHost),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	// Probe socket.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		return nil, err
	}
	return &DockerWatcher{
		cli:         cli,
		labelPrefix: labelPrefix,
		zone:        zone,
		resolver:    resolver,
		log:         log,
		publishIP:   publishIP,
		hostIP:      hostIP,
	}, nil
}

func (d *DockerWatcher) Close() error {
	if d.cli != nil {
		return d.cli.Close()
	}
	return nil
}

// Run performs the initial sync and then watches the event stream until ctx is done.
func (d *DockerWatcher) Run(ctx context.Context) error {
	if err := d.resync(ctx); err != nil {
		d.log.Error("initial sync failed", "err", err)
	}

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := d.streamEvents(ctx)
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			d.log.Error("event stream error, reconnecting", "err", err, "backoff", backoff)
		} else {
			d.log.Warn("event stream ended, reconnecting", "backoff", backoff)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (d *DockerWatcher) streamEvents(ctx context.Context) error {
	f := filters.NewArgs()
	f.Add("type", "container")
	f.Add("event", "start")
	f.Add("event", "die")
	f.Add("event", "stop")
	f.Add("event", "destroy")

	msgs, errs := d.cli.Events(ctx, types.EventsOptions{Filters: f})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			if err == nil || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case <-msgs:
			if err := d.resync(ctx); err != nil {
				d.log.Error("resync failed", "err", err)
			}
		}
	}
}

func (d *DockerWatcher) resync(ctx context.Context) error {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	containers, err := d.cli.ContainerList(listCtx, container.ListOptions{All: false})
	if err != nil {
		return err
	}

	desired := make(map[string]string)
	for _, c := range containers {
		fqdns := d.hostsFromLabels(c.Labels)
		if len(fqdns) == 0 {
			continue
		}

		var (
			ip  string
			err error
		)
		if d.publishIP == "host" {
			ip = d.hostIP
		} else {
			ip, err = d.containerIP(ctx, c.ID)
		}
		if err != nil || ip == "" {
			d.log.Warn("container has caddy label but no resolvable IP",
				"id", shortID(c.ID), "name", containerName(c.Names), "err", err)
			continue
		}
		for _, fq := range fqdns {
			desired[fq] = ip
		}
	}

	d.resolver.Sync(desired)
	return nil
}

func (d *DockerWatcher) hostsFromLabels(labels map[string]string) []string {
	var out []string
	seen := make(map[string]struct{})
	for k, v := range labels {
		if !labelMatches(k, d.labelPrefix) {
			continue
		}
		fq := fqdnFromLabel(v, d.zone)
		if fq == "" {
			continue
		}
		if _, ok := seen[fq]; ok {
			continue
		}
		seen[fq] = struct{}{}
		out = append(out, fq)
	}
	return out
}

// labelMatches matches the bare prefix or prefix followed by a dotted integer
// index (e.g. `caddy`, `caddy.0`, `caddy.12`). Sub-directive labels like
// `caddy.tls` or `caddy.0.respond` are rejected — they don't carry hostnames.
func labelMatches(key, prefix string) bool {
	return labelRegex(prefix).MatchString(key)
}

var (
	labelRegexCache   = map[string]*regexp.Regexp{}
	labelRegexCacheMu sync.Mutex
)

func labelRegex(prefix string) *regexp.Regexp {
	labelRegexCacheMu.Lock()
	defer labelRegexCacheMu.Unlock()
	if re, ok := labelRegexCache[prefix]; ok {
		return re
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `(\.\d+)?$`)
	labelRegexCache[prefix] = re
	return re
}

func (d *DockerWatcher) containerIP(ctx context.Context, id string) (string, error) {
	inspectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := d.cli.ContainerInspect(inspectCtx, id)
	if err != nil {
		return "", err
	}
	if info.NetworkSettings == nil {
		return "", nil
	}
	for _, n := range info.NetworkSettings.Networks {
		if n != nil && n.IPAddress != "" {
			return n.IPAddress, nil
		}
	}
	return "", nil
}

func parseIP(s string) net.IP {
	return net.ParseIP(s)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}
