package group

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
)

func validateHealthChecks(checks []option.OutboundHealthCheck) error {
	for i, check := range checks {
		link, err := url.Parse(check.URL)
		if err != nil || link.Hostname() == "" || link.User != nil || link.Scheme != "https" {
			return E.New("check ", i, " requires an HTTPS URL without credentials")
		}
		if check.IPVersion != 0 && check.IPVersion != 4 && check.IPVersion != 6 {
			return E.New("check ", i, " has invalid ip_version")
		}
		if check.StatusCode != 0 && (check.StatusCode < 200 || check.StatusCode >= 300) {
			return E.New("check ", i, " requires a 2xx status_code")
		}
	}
	return nil
}

func probeOutbound(ctx context.Context, member adapter.Outbound, check option.OutboundHealthCheck) error {
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
