package group

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

type fallbackCollector struct {
	fallback          *Fallback
	selected, healthy *prometheus.Desc
	probeSuccess      *prometheus.Desc
}

func newFallbackCollector(f *Fallback) *fallbackCollector {
	labels := prometheus.Labels{"group": f.Tag()}
	return &fallbackCollector{
		fallback:     f,
		selected:     prometheus.NewDesc("sing_box_fallback_selected", "Whether the outbound is currently selected.", []string{"outbound"}, labels),
		healthy:      prometheus.NewDesc("sing_box_fallback_healthy", "Whether the outbound is healthy after applying failure and recovery thresholds.", []string{"outbound"}, labels),
		probeSuccess: prometheus.NewDesc("sing_box_fallback_probe_success", "Result of the most recent HTTPS probe.", []string{"outbound", "check", "url", "ip_version"}, labels),
	}
}

func (c *fallbackCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.selected
	ch <- c.healthy
	ch <- c.probeSuccess
}

func (c *fallbackCollector) Collect(ch chan<- prometheus.Metric) {
	f := c.fallback
	status := f.status.Load()
	for i, tag := range f.tags {
		ch <- prometheus.MustNewConstMetric(c.selected, prometheus.GaugeValue, metricBool(i == status.selected), tag)
		member := status.members[i]
		if member.results != nil {
			ch <- prometheus.MustNewConstMetric(c.healthy, prometheus.GaugeValue, metricBool(!member.failed), tag)
		}
		for j, passed := range member.results {
			check := f.checks[j]
			ch <- prometheus.MustNewConstMetric(c.probeSuccess, prometheus.GaugeValue, metricBool(passed),
				tag, strconv.Itoa(j+1), check.URL, strconv.Itoa(check.IPVersion))
		}
	}
}

func metricBool(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
