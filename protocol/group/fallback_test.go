package group

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestFallbackRequiredChecksAndPriority(t *testing.T) {
	s := &fallbackStatus{members: make([]fallbackMemberStatus, 3)}
	up := []bool{true, true, true, true}
	ipv6Down := []bool{true, false, true, true}
	googleDown := []bool{false, false, true, true}
	for _, step := range []struct {
		name                 string
		proton, warp, direct []bool
		selected             int
	}{
		{"healthy", up, up, up, 0},
		{"one IPv6 failure", ipv6Down, up, up, 0},
		{"IPv6 recovers", up, up, up, 0},
		{"IPv6 fails once again", ipv6Down, up, up, 0},
		{"IPv6 fails twice", ipv6Down, up, up, 1},
		{"WARP loses Google once", ipv6Down, googleDown, googleDown, 1},
		{"both tunnels unhealthy even when direct checks fail", ipv6Down, googleDown, googleDown, 2},
		{"single good Proton cycle", up, up, up, 2},
		{"Proton recovers, despite equally healthy WARP", up, up, up, 0},
	} {
		s = advanceFallback(s, [][]bool{step.proton, step.warp, step.direct}, 2, 2)
		if s.selected != step.selected {
			t.Fatalf("%s: got member %d, want %d", step.name, s.selected, step.selected)
		}
	}
}

type testFallbackRoots struct {
	adapter.CertificateStore
	pool *x509.CertPool
}

func (c testFallbackRoots) Pool() *x509.CertPool { return c.pool }

type testFallbackMember struct {
	adapter.Outbound
	dial func(context.Context, string, M.Socksaddr) (net.Conn, error)
}

func (o testFallbackMember) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return o.dial(ctx, network, destination)
}

func TestFallbackProbeUsesMemberAndExpectedStatus(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Error("health probe must use HEAD")
		}
		switch r.URL.Path {
		case "/good":
			w.WriteHeader(http.StatusNoContent)
		case "/redirect":
			http.Redirect(w, r, "/good", http.StatusFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	ctx := service.ContextWith[adapter.CertificateStore](context.Background(), testFallbackRoots{pool: pool})
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	member := testFallbackMember{dial: func(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
		if network != "tcp" || !destination.Addr.Is4() {
			t.Errorf("incorrect dial: %s %s", network, destination)
		}
		return (&net.Dialer{}).DialContext(ctx, network, destination.String())
	}}
	for _, tc := range []struct {
		path      string
		ipVersion int
		wantError bool
	}{
		{"/good", 4, false},
		{"/redirect", 4, true},
		{"/wrong-code", 4, true},
		{"/good", 6, true},
	} {
		err := probeFallback(ctx, member, option.FallbackCheck{URL: server.URL + tc.path, IPVersion: tc.ipVersion, StatusCode: 204})
		if (err != nil) != tc.wantError {
			t.Errorf("%s IPv%d: got %v, want error %v", tc.path, tc.ipVersion, err, tc.wantError)
		}
	}
}

func TestFallbackMetricsDescribeActiveMemberAndFailedChecks(t *testing.T) {
	f := &Fallback{
		Adapter: outbound.NewAdapter(C.TypeFallback, "egress-out", nil, nil),
		tags:    []string{"test-tunnel", "direct-out"},
		checks:  []option.FallbackCheck{{URL: "https://example.com", IPVersion: 6}},
	}
	start := &fallbackStatus{members: make([]fallbackMemberStatus, 2)}
	f.status.Store(advanceFallback(start, [][]bool{{false}, {false}}, 1, 1))
	registry := prometheus.NewRegistry()
	if err := registry.Register(newFallbackCollector(f)); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range []string{
		`sing_box_fallback_selected{group="egress-out",outbound="direct-out"} 1`,
		`sing_box_fallback_probe_success{check="1",group="egress-out",ip_version="6",outbound="direct-out",url="https://example.com"} 0`,
	} {
		if !strings.Contains(w.Body.String(), line+"\n") {
			t.Fatalf("missing %s in %s", line, w.Body.String())
		}
	}
}

func TestFallbackWaitsForTenthFailedCycleByDefault(t *testing.T) {
	outbound, err := NewFallback(context.Background(), nil, nil, "egress", option.FallbackOutboundOptions{
		Outbounds: []string{"test-tunnel", "direct"},
		Checks:    []option.FallbackCheck{{URL: "https://example.com", IPVersion: 6}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f := outbound.(*Fallback)
	if f.interval != 30*time.Second {
		t.Fatalf("probe interval = %s, want 30s", f.interval)
	}
	state := f.status.Load()
	for cycle := 1; cycle <= 10; cycle++ {
		state = advanceFallback(state, [][]bool{{false}, {true}}, f.failureCount, f.recoveryCount)
		want := 0
		if cycle == 10 {
			want = 1
		}
		if state.selected != want {
			t.Fatalf("cycle %d: selected member %d, want %d", cycle, state.selected, want)
		}
	}
}
