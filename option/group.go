package option

import "github.com/sagernet/sing/common/json/badoption"

type SelectorOutboundOptions struct {
	Outbounds                 []string `json:"outbounds" reference:"outbound"`
	Default                   string   `json:"default,omitempty" reference:"outbound"`
	InterruptExistConnections bool     `json:"interrupt_exist_connections,omitempty"`
}

type URLTestOutboundOptions struct {
	Outbounds                 []string              `json:"outbounds" reference:"outbound"`
	URL                       string                `json:"url,omitempty"`
	Checks                    []OutboundHealthCheck `json:"checks,omitempty"`
	CheckTimeout              badoption.Duration    `json:"check_timeout,omitempty"`
	Interval                  badoption.Duration    `json:"interval,omitempty"`
	Tolerance                 uint16                `json:"tolerance,omitempty"`
	IdleTimeout               badoption.Duration    `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool                  `json:"interrupt_exist_connections,omitempty"`
}

type FallbackOutboundOptions struct {
	Outbounds     []string              `json:"outbounds" reference:"outbound"`
	Checks        []OutboundHealthCheck `json:"checks"`
	Interval      badoption.Duration    `json:"interval,omitempty"`
	Timeout       badoption.Duration    `json:"timeout,omitempty"`
	FailureCount  int                   `json:"failure_count,omitempty"`
	RecoveryCount int                   `json:"recovery_count,omitempty"`
}

type OutboundHealthCheck struct {
	URL        string `json:"url"`
	IPVersion  int    `json:"ip_version,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
}
