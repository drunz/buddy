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
	ttl     uint32
	log     *slog.Logger
}

func NewResolver(ttl uint32, log *slog.Logger) *Resolver {
	return &Resolver{
		records: make(map[string]string),
		ttl:     ttl,
		log:     log,
	}
}

// fqdnFromLabel parses a caddy label value into a fully qualified DNS name.
// Strips ports.
//
// If zones is non-empty, the label must fall within one of the zones: a bare
// hostname gets the first zone appended; a dotted name must equal or be a
// subdomain of one of the zones, otherwise "" is returned.
//
// If zones is empty, no filter is applied: the label is published verbatim
// (with a trailing dot).
func fqdnFromLabel(label string, zones []string) string {
	v := strings.TrimSpace(label)
	if v == "" {
		return ""
	}
	if idx := strings.Index(v, ":"); idx >= 0 {
		v = v[:idx]
	}
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, ".")
	if v == "" {
		return ""
	}

	lowerV := strings.ToLower(v)

	if len(zones) == 0 {
		return lowerV + "."
	}

	for _, z := range zones {
		zoneTrim := strings.ToLower(strings.TrimSuffix(z, "."))
		if lowerV == zoneTrim || strings.HasSuffix(lowerV, "."+zoneTrim) {
			return lowerV + "."
		}
	}
	if !strings.Contains(v, ".") {
		first := strings.TrimSuffix(zones[0], ".")
		return lowerV + "." + strings.ToLower(first) + "."
	}
	return ""
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
