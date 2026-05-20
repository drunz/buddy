package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/miekg/dns"
)

func main() {
	cfg := LoadConfig()
	log := newLogger(cfg.LogLevel)

	log.Info("starting buddy",
		"port", cfg.DNSPort,
		"zone", cfg.DNSZone,
		"ttl", cfg.DNSTTL,
		"docker_host", cfg.DockerHost,
		"label_prefix", cfg.CaddyLabelPrefix,
	)

	resolver := NewResolver(cfg.DNSZone, cfg.DNSTTL, log)

	watcher, err := NewDockerWatcher(cfg.DockerHost, cfg.CaddyLabelPrefix, cfg.DNSZone, resolver, log)
	if err != nil {
		log.Error("failed to connect to docker", "err", err)
		os.Exit(1)
	}
	defer watcher.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	mux := dns.NewServeMux()
	mux.HandleFunc(cfg.DNSZone, resolver.Handle)

	udpServer := &dns.Server{Addr: ":" + cfg.DNSPort, Net: "udp", Handler: mux}
	tcpServer := &dns.Server{Addr: ":" + cfg.DNSPort, Net: "tcp", Handler: mux}

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		log.Info("dns udp server listening", "addr", udpServer.Addr)
		if err := udpServer.ListenAndServe(); err != nil {
			log.Error("udp server error", "err", err)
			cancel()
		}
	}()

	go func() {
		defer wg.Done()
		log.Info("dns tcp server listening", "addr", tcpServer.Addr)
		if err := tcpServer.ListenAndServe(); err != nil {
			log.Error("tcp server error", "err", err)
			cancel()
		}
	}()

	go func() {
		defer wg.Done()
		if err := watcher.Run(ctx); err != nil && err != context.Canceled {
			log.Error("docker watcher exited", "err", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = udpServer.ShutdownContext(shutdownCtx)
	_ = tcpServer.ShutdownContext(shutdownCtx)

	wg.Wait()
}

func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	if level == "debug" {
		lvl = slog.LevelDebug
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}
