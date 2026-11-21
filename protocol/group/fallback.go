package group

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/service/metrics"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterFallback(registry *outbound.Registry) {
	outbound.Register[option.FallbackOutboundOptions](registry, C.TypeFallback, NewFallback)
}

var _ adapter.OutboundGroup = (*Fallback)(nil)

type Fallback struct {
	outbound.Adapter
	manager       adapter.OutboundManager
	logger        log.ContextLogger
	tags          []string
	members       []adapter.Outbound
	checks        []option.FallbackCheck
	interval      time.Duration
	timeout       time.Duration
	failureCount  int
	recoveryCount int
	status        atomic.Pointer[fallbackStatus]
}

type fallbackStatus struct {
	selected int
	members  []fallbackMemberStatus
}

type fallbackMemberStatus struct {
	failed      bool
	consecutive int
	results     []bool
}

func NewFallback(ctx context.Context, _ adapter.Router, logger log.ContextLogger, tag string, options option.FallbackOutboundOptions) (adapter.Outbound, error) {
	if len(options.Outbounds) < 2 {
		return nil, E.New("fallback requires at least two outbounds")
	}
	for i, tag := range options.Outbounds {
		if tag == "" || slices.Contains(options.Outbounds[:i], tag) {
			return nil, E.New("empty or duplicate fallback outbound tag: ", tag)
		}
	}
	if len(options.Checks) == 0 {
		return nil, E.New("fallback requires at least one check")
	}
	for i, check := range options.Checks {
		link, err := url.Parse(check.URL)
		if err != nil || link.Hostname() == "" || link.User != nil || link.Scheme != "https" {
			return nil, E.New("fallback check ", i, " requires an HTTPS URL without credentials")
		}
		if check.IPVersion != 0 && check.IPVersion != 4 && check.IPVersion != 6 {
			return nil, E.New("fallback check ", i, " has invalid ip_version")
		}
		if check.StatusCode != 0 && (check.StatusCode < 200 || check.StatusCode >= 300) {
			return nil, E.New("fallback check ", i, " requires a 2xx status_code")
		}
	}
	interval := time.Duration(options.Interval)
	if interval == 0 {
		interval = 30 * time.Second
	}
	timeout := time.Duration(options.Timeout)
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	if timeout <= 0 || timeout >= interval {
		return nil, E.New("fallback timeout must be positive and less than interval")
	}
	if options.FailureCount < 0 || options.RecoveryCount < 0 {
		return nil, E.New("fallback failure_count and recovery_count must be positive")
	}
	fallback := &Fallback{
		Adapter:       outbound.NewAdapter(C.TypeFallback, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		manager:       service.FromContext[adapter.OutboundManager](ctx),
		logger:        logger,
		tags:          options.Outbounds,
		checks:        options.Checks,
		interval:      interval,
		timeout:       timeout,
		failureCount:  options.FailureCount,
		recoveryCount: options.RecoveryCount,
	}
	if fallback.failureCount == 0 {
		fallback.failureCount = 10
	}
	if fallback.recoveryCount == 0 {
		fallback.recoveryCount = 2
	}
	fallback.status.Store(&fallbackStatus{members: make([]fallbackMemberStatus, len(options.Outbounds))})
	return fallback, nil
}

func (f *Fallback) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	switch stage {
	case adapter.StartStateStart:
		f.members = make([]adapter.Outbound, 0, len(f.tags))
		for _, tag := range f.tags {
			member, loaded := f.manager.Outbound(tag)
			if !loaded {
				return E.New("fallback outbound not found: ", tag)
			}
			f.members = append(f.members, member)
		}
		return metrics.Register(scope, newFallbackCollector(f))
	case adapter.StartStateStarted:
		done := make(chan struct{})
		scope.Add(func() error {
			<-done
			return nil
		})
		go func() {
			defer close(done)
			f.run(scope.Context())
		}()
	}
	return nil
}

func (f *Fallback) All() []string {
	return f.tags
}

func (f *Fallback) Selected(network string) adapter.Outbound {
	if len(f.members) == 0 {
		return nil
	}
	member := f.members[f.status.Load().selected]
	if !slices.Contains(member.Network(), network) {
		return nil
	}
	return member
}

func (f *Fallback) AttachConnection(io.Closer) func() {
	// Changing the selected member leaves established connections on their original member.
	return func() {}
}

func (f *Fallback) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	member := f.Selected(N.NetworkName(network))
	if member == nil {
		return nil, E.New("selected fallback outbound does not support ", network)
	}
	return member.DialContext(ctx, network, destination)
}

func (f *Fallback) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	member := f.Selected(N.NetworkUDP)
	if member == nil {
		return nil, E.New("selected fallback outbound does not support UDP")
	}
	return member.ListenPacket(ctx, destination)
}

func (f *Fallback) run(ctx context.Context) {
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()
	for {
		f.probe(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (f *Fallback) probe(ctx context.Context) {
	cycle, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	results := make([][]bool, len(f.members))
	var pending sync.WaitGroup
	for i, member := range f.members {
		results[i] = make([]bool, len(f.checks))
		for j, check := range f.checks {
			pending.Go(func() {
				err := probeFallback(cycle, member, check)
				results[i][j] = err == nil
				if err != nil && ctx.Err() == nil {
					f.logger.Debug("check ", j+1, " through ", member.Tag(), ": ", err)
				}
			})
		}
	}
	pending.Wait()
	if ctx.Err() != nil {
		return
	}
	previous := f.status.Load()
	next := advanceFallback(previous, results, f.failureCount, f.recoveryCount)
	f.status.Store(next)
	if next.selected != previous.selected {
		f.logger.Info("selected outbound changed from ", f.tags[previous.selected], " to ", f.tags[next.selected])
	}
}

func advanceFallback(previous *fallbackStatus, results [][]bool, failures, recoveries int) *fallbackStatus {
	next := &fallbackStatus{
		selected: len(results) - 1,
		members:  slices.Clone(previous.members),
	}
	for i, passed := range results {
		member := &next.members[i]
		member.results = passed
		failed := slices.Contains(passed, false)
		if failed == member.failed {
			member.consecutive = 0
		} else {
			member.consecutive++
			threshold := recoveries
			if failed {
				threshold = failures
			}
			if member.consecutive >= threshold {
				member.failed = failed
				member.consecutive = 0
			}
		}
		if !member.failed && i < next.selected {
			next.selected = i
		}
	}
	return next
}

func probeFallback(ctx context.Context, member adapter.Outbound, check option.FallbackCheck) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, check.URL, nil)
	if err != nil {
		return err
	}
	hostname := request.URL.Hostname()
	ipNetwork := "ip"
	if check.IPVersion != 0 {
		ipNetwork += strconv.Itoa(check.IPVersion)
	}
	// Resolve probe names on the node so the health checks do not depend on
	// the application's DNS service, which itself may dial this fallback.
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, ipNetwork, hostname)
	if err != nil {
		return err
	}
	transport := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{RootCAs: adapter.RootPoolFromContext(ctx)},
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			var errs []error
			for _, addr := range addresses {
				conn, err := member.DialContext(ctx, network, M.ParseSocksaddrHostPortStr(addr.Unmap().String(), port))
				if err == nil {
					return conn, nil
				}
				errs = append(errs, err)
			}
			if len(errs) == 0 {
				return nil, E.New("no addresses for ", hostname)
			}
			return nil, E.Errors(errs...)
		},
	}
	client := http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 ||
		(check.StatusCode != 0 && response.StatusCode != check.StatusCode) {
		return E.New("unexpected HTTP status ", response.StatusCode)
	}
	return nil
}
