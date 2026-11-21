# Fallback

This outbound is available in the skidunion fork. It selects the first healthy
outbound in the given order. The final member is always available as the last
attempt, even when its checks fail. TCP and UDP use the same selected member.
Switching the selection leaves established connections on their previous member.

```json
{
  "type": "fallback",
  "tag": "egress-out",
  "outbounds": ["proton-out", "warp-out", "direct-out"],
  "checks": [
    {"url": "https://www.google.com/generate_204", "ip_version": 4, "status_code": 204},
    {"url": "https://www.google.com/generate_204", "ip_version": 6, "status_code": 204}
  ],
  "interval": "30s",
  "timeout": "5s",
  "failure_count": 10,
  "recovery_count": 2,
  "metrics_listen": "127.0.0.1:9201"
}
```

Each cycle runs the HTTPS HEAD requests through every member, resolving the
target names with the system resolver. An omitted `ip_version` allows both
families. An omitted `status_code` accepts any 2xx response. All checks must
pass for a cycle to succeed. Checks start immediately. Members are eligible
in configured order until they reach the failure threshold.

The optional address serves read-only Prometheus metrics at `/metrics`.
A private listener or source filtering is recommended for remote scraping.
