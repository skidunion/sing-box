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
