package main

import "testing"

func TestListenAddrAndHealthURL(t *testing.T) {
	for name, c := range map[string]struct{ host, port, addr, health string }{
		"defaults":   {"", "", ":8080", "http://127.0.0.1:8080/healthz"},
		"port only":  {"", "9090", ":9090", "http://127.0.0.1:9090/healthz"},
		"host+port":  {"127.0.0.1", "9090", "127.0.0.1:9090", "http://127.0.0.1:9090/healthz"},
		"ipv6 host":  {"::1", "9090", "[::1]:9090", "http://127.0.0.1:9090/healthz"},
		"host alone": {"127.0.0.1", "", "127.0.0.1:8080", "http://127.0.0.1:8080/healthz"},
	} {
		t.Setenv("HOST", c.host)
		t.Setenv("PORT", c.port)
		if got := listenAddr(); got != c.addr {
			t.Errorf("%s: listenAddr() = %q, want %q", name, got, c.addr)
		}
		if got := healthURL(); got != c.health {
			t.Errorf("%s: healthURL() = %q, want %q", name, got, c.health)
		}
	}
}
