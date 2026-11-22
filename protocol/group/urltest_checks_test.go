package group

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type testDualStackMember struct {
	outbound.Adapter
	target     string
	delay      time.Duration
	ipv6Broken bool
}

func (m *testDualStackMember) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if m.ipv6Broken && destination.Addr.Is6() {
		return nil, errors.New("synthetic IPv6 outage")
	}
	if m.delay != 0 {
		timer := time.NewTimer(m.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return (&net.Dialer{}).DialContext(ctx, network, m.target)
}

func (m *testDualStackMember) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("synthetic peer only supports probe dials")
}

func TestCheckedURLTestKeepsFallbackOnProtonAfterPeerLosesIPv6(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	target := server.Listener.Addr().String()
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	required := []option.OutboundHealthCheck{
		{URL: server.URL, IPVersion: 4, StatusCode: 204},
		{URL: "https://" + net.JoinHostPort("::1", port), IPVersion: 6, StatusCode: 204},
	}
	newPeer := func(tag string, delay time.Duration) *testDualStackMember {
		return &testDualStackMember{
			Adapter: outbound.NewAdapter(C.TypeWireGuard, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
			target:  target,
			delay:   delay,
		}
	}
	fast := newPeer("proton-fast", 0)
	slow := newPeer("proton-slow", 150*time.Millisecond)
	warp := newPeer("warp", 0)
	direct := newPeer("direct", 0)

	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	ctx := service.ContextWith[adapter.CertificateStore](context.Background(), testFallbackRoots{pool: pool})
	logger := log.NewNOPFactory().Logger()
	inner := &URLTest{
		Adapter: outbound.NewAdapter(C.TypeURLTest, "proton-auto", []string{N.NetworkTCP, N.NetworkUDP}, nil),
		group: &URLTestGroup{
			logger:         logger,
			outbounds:      []adapter.Outbound{fast, slow},
			history:        urltest.NewHistoryStorage(),
			interruptGroup: interrupt.NewGroup(),
			tolerance:      50,
			checked:        &checkedURLTest{checks: required, timeout: time.Second},
		},
	}
	outer := &Fallback{
		logger:        logger,
		tags:          []string{"proton-auto", "warp", "direct"},
		members:       []adapter.Outbound{inner, warp, direct},
		checks:        required,
		timeout:       time.Second,
		failureCount:  1,
		recoveryCount: 2,
	}
	outer.status.Store(&fallbackStatus{members: make([]fallbackMemberStatus, 3)})

	for _, step := range []struct {
		name           string
		fastIPv6Broken bool
		slowIPv6Broken bool
		expectedProton adapter.Outbound
		expectedEgress adapter.Outbound
	}{
		{"fastest eligible peer", false, false, fast, inner},
		{"fast peer loses IPv6", true, false, slow, inner},
		{"both peers lose IPv6", true, true, nil, warp},
	} {
		t.Run(step.name, func(t *testing.T) {
			fast.ipv6Broken = step.fastIPv6Broken
			slow.ipv6Broken = step.slowIPv6Broken
			inner.group.runCheckedURLTest(ctx)
			outer.probe(ctx)
			for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
				if selected := inner.Selected(network); selected != step.expectedProton {
					t.Errorf("selected Proton over %s = %v, want %v", network, selected, step.expectedProton)
				}
				if selected := outer.Selected(network); selected != step.expectedEgress {
					t.Errorf("selected egress over %s = %v, want %v", network, selected, step.expectedEgress)
				}
			}
		})
	}
}

func TestCheckedURLTestKeepsHealthyPeerWithinTolerance(t *testing.T) {
	primary := &testDualStackMember{}
	reserve := &testDualStackMember{}
	members := []adapter.Outbound{primary, reserve}
	delays := []uint16{50, 100}
	if selected := selectCheckedURLTest(members, delays, reserve, 50); selected != reserve {
		t.Fatal("healthy peer inside tolerance was replaced")
	}
	if selected := selectCheckedURLTest(members, delays, reserve, 49); selected != primary {
		t.Fatal("healthy peer beyond tolerance was retained")
	}
}
