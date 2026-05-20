package main

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

type Resolver struct {
	mu      sync.RWMutex
	records map[string]string
	zone    string
	ttl     uint32
	log     *slog.Logger
}

func NewResolver(zone string, ttl uint32, log *slog.Logger) *Resolver {
	return &Resolver{
		records: make(map[string]string),
		zone:    zone,
		ttl:     ttl,
		log:     log,
	}
}

// fqdnFromLabel parses a caddy label value into a fully qualified DNS name
// within the given zone. Strips ports; bare hostnames get the zone appended.
// Returns "" if the label is empty or names a host outside the zone.
func fqdnFromLabel(label, zone string) string {
	v := strings.TrimSpace(label)
	if v == "" {
		return ""
	}
	// Strip everything after the first colon (port).
	if idx := strings.Index(v, ":"); idx >= 0 {
		v = v[:idx]
	}
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, ".")
	if v == "" {
		return ""
	}

	zoneTrim := strings.TrimSuffix(zone, ".")
	lowerV := strings.ToLower(v)
	lowerZone := strings.ToLower(zoneTrim)

	var fqdn string
	switch {
	case lowerV == lowerZone || strings.HasSuffix(lowerV, "."+lowerZone):
		fqdn = v + "."
	case !strings.Contains(v, "."):
		fqdn = v + "." + zoneTrim + "."
	default:
		// Dotted name outside the configured zone — don't publish it.
		return ""
	}
	return strings.ToLower(fqdn)
}

// Sync replaces the current records with the desired set, logging
// each add and remove.
func (r *Resolver) Sync(desired map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for fqdn, ip := range desired {
		if cur, ok := r.records[fqdn]; !ok || cur != ip {
			r.log.Info("dns record added", "fqdn", fqdn, "ip", ip)
			r.records[fqdn] = ip
		}
	}
	for fqdn, ip := range r.records {
		if _, ok := desired[fqdn]; !ok {
			r.log.Info("dns record removed", "fqdn", fqdn, "ip", ip)
			delete(r.records, fqdn)
		}
	}
}

func (r *Resolver) Lookup(fqdn string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ip, ok := r.records[strings.ToLower(fqdn)]
	return ip, ok
}

func (r *Resolver) Snapshot() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.records))
	for k, v := range r.records {
		out[k] = v
	}
	return out
}

func (r *Resolver) Handle(w dns.ResponseWriter, req *dns.Msg) {
	defer func() {
		if rec := recover(); rec != nil {
			r.log.Error("panic in dns handler", "panic", rec)
			m := new(dns.Msg)
			m.SetRcode(req, dns.RcodeServerFailure)
			_ = w.WriteMsg(m)
		}
	}()

	m := new(dns.Msg)
	m.SetReply(req)
	m.Authoritative = true
	m.RecursionAvailable = false

	if len(req.Question) == 0 {
		m.SetRcode(req, dns.RcodeFormatError)
		_ = w.WriteMsg(m)
		return
	}

	q := req.Question[0]
	name := strings.ToLower(q.Name)

	if q.Qtype != dns.TypeA {
		// Authoritative empty answer for non-A inside our zone, NXDOMAIN otherwise.
		if _, ok := r.Lookup(name); ok {
			_ = w.WriteMsg(m)
			return
		}
		m.SetRcode(req, dns.RcodeNameError)
		_ = w.WriteMsg(m)
		return
	}

	ip, ok := r.Lookup(name)
	if !ok {
		m.SetRcode(req, dns.RcodeNameError)
		_ = w.WriteMsg(m)
		return
	}

	rr := &dns.A{
		Hdr: dns.RR_Header{
			Name:   q.Name,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    r.ttl,
		},
		A: parseIP(ip),
	}
	m.Answer = append(m.Answer, rr)
	_ = w.WriteMsg(m)
}
