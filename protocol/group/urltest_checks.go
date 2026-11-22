package group

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/batch"
)

type checkedURLTest struct {
	checks   []option.OutboundHealthCheck
	timeout  time.Duration
	selected common.TypedValue[adapter.Outbound]
}

func (g *URLTestGroup) runCheckedURLTest(parent context.Context) map[string]uint16 {
	delays := make([]uint16, len(g.outbounds))
	checks, ctx := batch.New[any](parent, batch.WithConcurrencyNum[any](10))
	for i, member := range g.outbounds {
		checks.Go(member.Tag(), func() (any, error) {
			probeCtx, cancel := context.WithTimeout(ctx, g.checked.timeout)
			defer cancel()
			delays[i] = g.checkURLTestMember(probeCtx, member)
			return nil, nil
		})
	}
	checks.Wait()
	results := make(map[string]uint16, len(g.outbounds))
	if parent.Err() != nil {
		return results
	}

	for i, member := range g.outbounds {
		if delays[i] == 0 {
			g.history.DeleteURLTestHistory(member.Tag())
			continue
		}
		results[member.Tag()] = delays[i]
		g.history.StoreURLTestHistory(member.Tag(), &adapter.URLTestHistory{Time: time.Now(), Delay: delays[i]})
	}
	current := g.checked.selected.Load()
	next := selectCheckedURLTest(g.outbounds, delays, current, g.tolerance)
	if current != next {
		g.checked.selected.Store(next)
		if current != nil {
			g.interruptGroup.Interrupt(g.interruptExternalConnections)
		}
		g.history.NotifyUpdated()
	}
	return results
}

func (g *URLTestGroup) checkURLTestMember(ctx context.Context, member adapter.Outbound) uint16 {
	var pending sync.WaitGroup
	results := make([]error, len(g.checked.checks))
	var delay time.Duration
	for i, check := range g.checked.checks {
		pending.Go(func() {
			start := time.Now()
			results[i] = probeOutbound(ctx, member, check)
			if i == 0 {
				delay = time.Since(start)
			}
		})
	}
	pending.Wait()
	for i, err := range results {
		if err != nil {
			g.logger.Debug("check ", i+1, " through ", member.Tag(), ": ", err)
			return 0
		}
	}
	return uint16(min(math.MaxUint16, max(1, delay.Milliseconds())))
}

func selectCheckedURLTest(members []adapter.Outbound, delays []uint16, current adapter.Outbound, tolerance uint16) adapter.Outbound {
	var fastest adapter.Outbound
	var fastestDelay, currentDelay uint16
	for i, member := range members {
		delay := delays[i]
		if delay == 0 {
			continue
		}
		if fastest == nil || delay < fastestDelay {
			fastest = member
			fastestDelay = delay
		}
		if member == current {
			currentDelay = delay
		}
	}
	if currentDelay > 0 && int(currentDelay) <= int(fastestDelay)+int(tolerance) {
		return current
	}
	return fastest
}
