package group

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
)

func (f *Fallback) startMetrics(scope *adapter.Scope) error {
	listener, err := net.Listen("tcp", f.metricsListen)
	if err != nil {
		return E.Cause(err, "listen for fallback metrics")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", f.writeMetrics)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	scope.Add(server.Close)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			f.logger.Error("serve fallback metrics: ", err)
		}
	}()
	return nil
}

func (f *Fallback) writeMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	status := f.status.Load()
	var metrics strings.Builder
	metrics.WriteString("# TYPE sing_box_fallback_selected gauge\n")
	for i, tag := range f.tags {
		fmt.Fprintf(&metrics, "sing_box_fallback_selected{outbound=%q} %d\n", tag, metricBool(i == status.selected))
	}
	metrics.WriteString("# TYPE sing_box_fallback_healthy gauge\n")
	for i, tag := range f.tags {
		member := status.members[i]
		if member.results != nil {
			fmt.Fprintf(&metrics, "sing_box_fallback_healthy{outbound=%q} %d\n", tag, metricBool(!member.failed))
		}
	}
	metrics.WriteString("# TYPE sing_box_fallback_probe_success gauge\n")
	for i, tag := range f.tags {
		for j, passed := range status.members[i].results {
			check := f.checks[j]
			fmt.Fprintf(&metrics, "sing_box_fallback_probe_success{outbound=%q,url=%q,ip_version=%q} %d\n", tag, check.URL, fmt.Sprint(check.IPVersion), metricBool(passed))
		}
	}
	_, _ = w.Write([]byte(metrics.String()))
}

func metricBool(value bool) int {
	if value {
		return 1
	}
	return 0
}
