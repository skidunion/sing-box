### Structure

```json
{
  "type": "urltest",
  "tag": "auto",
  
  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "url": "",
  "interval": "",
  "tolerance": 0,
  "idle_timeout": "",
  "interrupt_exist_connections": false
}
```

### Fields

#### outbounds

==Required==

List of outbound tags to test.

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### interval

The test interval. `3m` will be used if empty.

#### tolerance

The test tolerance in milliseconds. `50` will be used if empty.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.

### Required checks

The skidunion fork also accepts an optional `checks` list in place of `url`.

```json
{
  "type": "urltest",
  "tag": "proton-auto",
  "outbounds": ["proton-wg-01", "proton-wg-02"],
  "interval": "30s",
  "tolerance": 50,
  "check_timeout": "5s",
  "checks": [
    {"url": "https://www.google.com/generate_204", "ip_version": 4, "status_code": 204},
    {"url": "https://www.google.com/generate_204", "ip_version": 6, "status_code": 204}
  ]
}
```

Every check must pass on the same member. Each check uses HTTPS HEAD and may
pin IPv4 or IPv6 and require a specific 2xx status. The first check supplies the
delay. The group keeps its healthy selection within `tolerance` and switches
immediately when it observes a failed check on the current member and finds a
healthy alternative. All new connections share one selection. The group is
unavailable until a member passes, and whenever every member fails the checks.
`check_timeout` is per member and defaults to five seconds.
