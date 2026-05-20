package main

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/miekg/dns"
)

func TestFqdnFromLabel(t *testing.T) {
	tests := []struct {
		name  string
		label string
		zone  string
		want  string
	}{
		{"simple host", "myapp", "local.lan.", "myapp.local.lan."},
		{"host with port", "myapp:80", "local.lan.", "myapp.local.lan."},
		{"fqdn with zone suffix", "myapp.local.lan", "local.lan.", "myapp.local.lan."},
		{"fqdn with zone and port", "myapp.local.lan:8080", "local.lan.", "myapp.local.lan."},
		{"dotted out of zone dropped", "api.myapp", "local.lan.", ""},
		{"subdomain in zone", "api.myapp.local.lan", "local.lan.", "api.myapp.local.lan."},
		{"explicit out of zone fqdn dropped", "foo.example.com", "local.lan.", ""},
		{"trailing dot", "myapp.local.lan.", "local.lan.", "myapp.local.lan."},
		{"uppercase normalized", "MyApp.Local.LAN", "local.lan.", "myapp.local.lan."},
		{"empty", "", "local.lan.", ""},
		{"whitespace", "   ", "local.lan.", ""},
		{"only port", ":80", "local.lan.", ""},
		{"zone equals host", "local.lan", "local.lan.", "local.lan."},
		{"different zone", "myapp", "example.com.", "myapp.example.com."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fqdnFromLabel(tt.label, tt.zone)
			if got != tt.want {
				t.Errorf("fqdnFromLabel(%q,%q) = %q, want %q", tt.label, tt.zone, got, tt.want)
			}
		})
	}
}

func TestLabelMatches(t *testing.T) {
	tests := []struct {
		key, prefix string
		want        bool
	}{
		{"caddy", "caddy", true},
		{"caddy.0", "caddy", true},
		{"caddy.12", "caddy", true},
		{"caddy.tls", "caddy", false},
		{"caddy.0.respond", "caddy", false},
		{"caddy.1.something", "caddy", false},
		{"caddy_0", "caddy", false},
		{"caddyfoo", "caddy", false},
		{"other", "caddy", false},
		{"", "caddy", false},
	}
	for _, tt := range tests {
		got := labelMatches(tt.key, tt.prefix)
		if got != tt.want {
			t.Errorf("labelMatches(%q,%q) = %v, want %v", tt.key, tt.prefix, got, tt.want)
		}
	}
}

func newTestResolver() *Resolver {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewResolver("local.lan.", 30, log)
}

func TestResolverSyncAddRemove(t *testing.T) {
	r := newTestResolver()

	r.Sync(map[string]string{
		"a.local.lan.": "10.0.0.1",
		"b.local.lan.": "10.0.0.2",
	})
	snap := r.Snapshot()
	if len(snap) != 2 || snap["a.local.lan."] != "10.0.0.1" || snap["b.local.lan."] != "10.0.0.2" {
		t.Fatalf("after initial sync, got %v", snap)
	}

	// Update one, remove one, add one.
	r.Sync(map[string]string{
		"a.local.lan.": "10.0.0.99",
		"c.local.lan.": "10.0.0.3",
	})
	snap = r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 records, got %v", snap)
	}
	if snap["a.local.lan."] != "10.0.0.99" {
		t.Errorf("expected update, got %v", snap["a.local.lan."])
	}
	if snap["c.local.lan."] != "10.0.0.3" {
		t.Errorf("expected new record, got %v", snap)
	}
	if _, ok := snap["b.local.lan."]; ok {
		t.Errorf("expected b removed, still present")
	}
}

type fakeWriter struct {
	msg *dns.Msg
}

func (f *fakeWriter) LocalAddr() net.Addr         { return &net.UDPAddr{} }
func (f *fakeWriter) RemoteAddr() net.Addr        { return &net.UDPAddr{} }
func (f *fakeWriter) WriteMsg(m *dns.Msg) error   { f.msg = m; return nil }
func (f *fakeWriter) Write([]byte) (int, error)   { return 0, nil }
func (f *fakeWriter) Close() error                { return nil }
func (f *fakeWriter) TsigStatus() error           { return nil }
func (f *fakeWriter) TsigTimersOnly(bool)         {}
func (f *fakeWriter) Hijack()                     {}

func TestHandleA(t *testing.T) {
	r := newTestResolver()
	r.Sync(map[string]string{"myapp.local.lan.": "10.0.0.5"})

	req := new(dns.Msg)
	req.SetQuestion("myapp.local.lan.", dns.TypeA)

	w := &fakeWriter{}
	r.Handle(w, req)

	if w.msg == nil {
		t.Fatal("no response written")
	}
	if !w.msg.Authoritative {
		t.Error("expected Authoritative flag set")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("expected NOERROR, got %d", w.msg.Rcode)
	}
	if len(w.msg.Answer) != 1 {
		t.Fatalf("expected 1 answer, got %d", len(w.msg.Answer))
	}
	a, ok := w.msg.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected *dns.A, got %T", w.msg.Answer[0])
	}
	if a.A.String() != "10.0.0.5" {
		t.Errorf("expected 10.0.0.5, got %s", a.A.String())
	}
	if a.Hdr.Ttl != 30 {
		t.Errorf("expected TTL 30, got %d", a.Hdr.Ttl)
	}
}

func TestHandleNXDOMAIN(t *testing.T) {
	r := newTestResolver()
	req := new(dns.Msg)
	req.SetQuestion("unknown.local.lan.", dns.TypeA)
	w := &fakeWriter{}
	r.Handle(w, req)
	if w.msg == nil {
		t.Fatal("no response")
	}
	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN, got %d", w.msg.Rcode)
	}
	if !w.msg.Authoritative {
		t.Error("expected Authoritative flag set")
	}
}

func TestHandleNonA(t *testing.T) {
	r := newTestResolver()
	r.Sync(map[string]string{"myapp.local.lan.": "10.0.0.5"})

	req := new(dns.Msg)
	req.SetQuestion("myapp.local.lan.", dns.TypeAAAA)
	w := &fakeWriter{}
	r.Handle(w, req)
	if w.msg == nil {
		t.Fatal("no response")
	}
	// Known name, non-A: NOERROR with no answers.
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("expected NOERROR for known name non-A, got %d", w.msg.Rcode)
	}
	if len(w.msg.Answer) != 0 {
		t.Errorf("expected no answers, got %d", len(w.msg.Answer))
	}
}

func TestHandleCaseInsensitive(t *testing.T) {
	r := newTestResolver()
	r.Sync(map[string]string{"myapp.local.lan.": "10.0.0.5"})

	req := new(dns.Msg)
	req.SetQuestion("MyApp.Local.LAN.", dns.TypeA)
	w := &fakeWriter{}
	r.Handle(w, req)
	if w.msg == nil || w.msg.Rcode != dns.RcodeSuccess || len(w.msg.Answer) != 1 {
		t.Fatalf("expected successful answer, got %+v", w.msg)
	}
}
