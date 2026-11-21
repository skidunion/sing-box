# Metrics

The skidunion fork provides a Prometheus listener at `/metrics` through an
entry in the top-level `services` list.

```json
{
  "type": "metrics",
  "tag": "prometheus",
  "listen": "127.0.0.1",
  "listen_port": 9201
}
```

See [Listen Fields](/configuration/shared/listen/) for address options.
The default address is loopback. A listen port is required. Scrapes use
read-only HTTP without authentication.

The [Fallback outbound](/configuration/outbound/fallback/) currently
provides the metrics below, with the outbound group identified by its tag
in the `group` label.

- `sing_box_fallback_selected` reports the active member.
- `sing_box_fallback_healthy` reports health after applying the thresholds.
- `sing_box_fallback_probe_success` reports the latest result for each probe.
