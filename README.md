# GuardDNS

GuardDNS is a fail-closed anti-pollution split DNS container for RouterOS,
Mihomo, and ordinary Docker hosts. It is designed as a smaller, auditable
successor to monolithic DNS bundles.

It combines:

- MosDNS 5.3.4 for routing, validation gates, and isolated caches.
- Alpine-packaged Unbound 1.25.1 for encrypted real-IP DNS with local DNSSEC
  validation and caching.
- A second Unbound instance that resolves recursively for CN classification,
  because a mainland name is only recognisable when the question is asked from
  the deployment's own network.
- An in-process loopback bridge that prefers NextDNS and Quad9 DoH through
  Mihomo, with direct Cloudflare and 360 DoH as emergency fallbacks.
- Optional Mihomo DNS integration for validated fake-IP.
- Functional Docker health checks and Prometheus runtime metrics.

## Why it is stricter

| Property | GuardDNS behavior |
| --- | --- |
| Unknown domains | Resolve once, accept CN IPs locally, and send validated NON-CN A answers to Mihomo when enabled |
| Global DNS | Mihomo DNS when AUTO_FORWARD is enabled; built-in encrypted real IP otherwise and during outage |
| Mainland DNS | Recursive Unbound decides CN membership; its answer is served only when the A records are CN, and is discarded otherwise |
| Fake-IP | Classified-global names go straight to Mihomo; unknown names reach it only after their real answer proves NON-CN |
| DNSSEC failure | Preserved as `SERVFAIL`, never converted to fake-IP |
| Cache | Unbound and the secure-real path cache only real answers; fake-IP is never cached by GuardDNS |
| Encrypted upstream | The Go bridge transports Unbound queries over DoH/443; Unbound validates DNSSEC locally |
| Runtime state | No Redis and no runtime mutation of upstream rule files |
| Supply chain | Pinned Go module graph, verified rule checksums, CI SBOM and provenance |

The routing policy is intentionally fail-closed:

```text
LAN -> RouterOS -> GuardDNS :53
                     |
                     +-- known global -> Mihomo fake-IP
                     |                    \-- Mihomo unusable -> encrypted real IP
                     |
                     +-- unknown -> recursive lookup -> CN IP -> return it
                                                      \-- otherwise -> discard,
                                                          re-resolve encrypted
                                                          -> optional fake-IP

Mihomo real DNS upstream -> GuardDNS :5304 -> validating Unbound -> DoH/443
```

## Quick start

Secure real-IP mode:

```sh
docker run -d \
  --name guarddns \
  --restart unless-stopped \
  -v ./data:/data \
  -p 53:53/udp -p 53:53/tcp \
  -p 5304:5304/udp -p 5304:5304/tcp \
  -p 127.0.0.1:5308:5308/tcp \
  ghcr.io/hyird/guarddns:latest
```

Mihomo fake-IP mode:

```sh
docker run -d \
  --name guarddns \
  --restart unless-stopped \
  -v ./data:/data \
  -e AUTO_FORWARD=172.16.0.101 \
  -p 53:53/udp -p 53:53/tcp \
  -p 5304:5304/udp -p 5304:5304/tcp \
  -p 127.0.0.1:5308:5308/tcp \
  ghcr.io/hyird/guarddns:latest
```

Port `53` provides split DNS and optional fake-IP. Port `5304` always returns a
real answer through encrypted DNS, making it safe as Mihomo's upstream.

## Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `LOG_LEVEL` | `warn` | `debug`, `info`, `warn`, or `error` |
| `AUTO_FORWARD` | `no` | `no` or Mihomo DNS `host[:port]`; the port defaults to `53` |

Only these two operational choices are configurable. IPv6 is always disabled.
Listeners are fixed at `0.0.0.0:53`, `0.0.0.0:5304`, and `0.0.0.0:5308`;
the timezone is `Asia/Shanghai`, and the secure real-answer cache size is
`8192`. Domain lists are fast paths. Any unclassified name is resolved once
and its real A response is checked against the CN CIDR set; CN answers are
returned directly and NON-CN answers enter the optional Mihomo path. This
avoids racing a healthy but slower CN response against Mihomo's fake-IP
response.

`AUTO_FORWARD` accepts `no`, a host/IPv4 address, or `host:port`. The DNS port
defaults to `53` when omitted. Setting an address sends validated non-CN A
queries to that Mihomo DNS service for fake-IP; SOCKS5 is not used. GuardDNS
first verifies the domain with its built-in encrypted real-IP resolver. If
Mihomo DNS fails, it reuses that same real answer without another lookup.
Failures use exponential retry delays from `1s` to `5min`; while the circuit is
open, queries bypass Mihomo immediately. A half-open probe restores forwarding
automatically after recovery. Set it to `no` to use encrypted real IP only.
The built-in real-IP path uses independent providers over authenticated
DNS-over-HTTPS. When `AUTO_FORWARD` is enabled, GuardDNS asks that same Mihomo
DNS endpoint for the provider's fake-IP and connects over HTTPS with the
provider hostname still verified by TLS. This keeps global encrypted DNS on
the Mihomo path without adding SOCKS5 or another setting. NextDNS is preferred,
then Quad9; failures enter jittered exponential backoff and recover
automatically. Direct Cloudflare and 360 DoH remain ordered emergency
fallbacks. The Go process transports DNS wire messages between the
loopback-only Unbound forwarder and the providers; Unbound validates DNSSEC
locally before MosDNS may pass an A query to Mihomo. Provider hostname
bootstrap queries use TCP to the Mihomo DNS endpoint, avoiding UDP loss during
cold connection setup.

This intentionally keeps the central
[PaoPaoDNS](https://github.com/kkkgo/PaoPaoDNS) behavior—real lookup, CN IP
classification, then optional custom forwarding for NON-CN—while reducing its
`CUSTOM_FORWARD` plus `AUTO_FORWARD=yes` pair to one
`AUTO_FORWARD=host[:port]` setting. Redis, SOCKS5, runtime update switches, and
IPv6 modes are omitted; the existing real response is reused for seamless
fallback instead of being queried again.

Logs always go to the container's standard output/error stream and are never
written to a fixed file. `LOG_LEVEL` controls supervisor, MosDNS, and Unbound
verbosity through the same setting.

## Health and metrics

The Go entrypoint independently supervises MosDNS and Unbound. If either child
exits, it is restarted with jittered exponential delays from `1s` to `30s`;
the container stays up and the other resolver continues serving what it can.
The container health check verifies `/plugins/guarddns/readyz` and then
performs a real A query through the secure `127.0.0.1:5304` listener. A
restarting Unbound may report `degraded` but stays healthy while encrypted DNS
still works; missing MosDNS, stale supervisor state, an unavailable DoH bridge,
or an unusable secure DNS path reports unhealthy.

The supervisor exposes separate operational layers:

- `/plugins/guarddns/livez` checks fresh supervisor state and the MosDNS
  process.
- `/plugins/guarddns/readyz` adds the DoH bridge and resolver dependencies;
  `/healthz` remains an alias for compatibility.
- `/plugins/guarddns/dependencies` returns the component and per-provider DoH
  state as JSON.

Supervisor state is sent to MosDNS through the Unix datagram socket
`/run/guarddns/supervisor.sock`. This does not create another TCP/UDP listener.
The non-standard ports form one consecutive, role-ordered block:

| Port | Scope | Role |
| --- | --- | --- |
| `5304` | Exposed DNS | Encrypted real-IP listener for Mihomo |
| `5305` | Loopback only | Recursive CN classifier |
| `5306` | Loopback only | Validating Unbound |
| `5307` | Loopback only | In-process DoH bridge |
| `5308` | HTTP | Health, metrics, and profiling |

The normal classification path starts as `:53 -> :5305`. CN answers return
there; NON-CN answers continue through `:5306 -> :5307`. Mihomo's real-IP
queries enter at `:5304` and continue through `:5306 -> :5307`.

Only `5304` and `5308` are exposed by the image. All private DNS hops remain
bound to loopback.

The classifier resolves in plaintext, since a delegation walk is what makes a
mainland answer mainland. It is only ever asked whether a name is CN: its reply
is served when the addresses are CN and discarded otherwise, so a poisoned
answer for a foreign name cannot reach a client. If it stops, the container
reports `degraded` and mainland names fall back to the encrypted path, which
resolves them correctly but from overseas.

Prometheus metrics are available at `/metrics` on the fixed container listener
`0.0.0.0:5308`. The Docker examples expose it only on host loopback:

```text
http://127.0.0.1:5308/metrics
```

In addition to Go, process, cache, and tagged upstream metrics, GuardDNS exports
end-to-end counters and latency histograms with the collector names `main` and
`secure`. MosDNS upstream metrics distinguish `unbound`, `unbound_secure`, and
`auto_forward`. Expected TCP client disconnects have their own
`mosdns_metrics_collector_canceled_total` and
`mosdns_guarddns_client_cancel_events_total` counters; they do not increment
DNS `err_total` or produce warning-log noise. Routing decisions are labeled in
`mosdns_guarddns_decisions_total`. Supervisor metrics begin with
`mosdns_guarddns_component_`; per-provider requests, success, failure, latency,
and backoff begin with `mosdns_guarddns_doh_upstream_`; circuit state, retry
delay, failures, and bypasses begin with `mosdns_guarddns_circuit_`.

MosDNS also exposes profiling handlers under `/debug/pprof` on the same HTTP
listener. Never publish this port to an untrusted network; restrict it with a
host binding or firewall.

## Custom rules

The `/data` volume is initialized with:

- `force-secure.txt`: always return encrypted real IP, bypassing fake-IP.
- `force-fakeip.txt`: force the global/fake-IP path.
- `force-direct.txt`: force validating Unbound without geographic filtering.

Unbound's writable DNSSEC trust anchor is kept under
`/run/guarddns/unbound`, not in the persistent rule volume.

Rules use MosDNS domain syntax:

```text
domain:example.com
full:www.example.com
keyword:example
regexp:^api[0-9]+\.example\.com$
```

Restart the container after editing rule files.

## RouterOS and Mihomo

For the existing `172.16.0.0/16` container bridge:

- GuardDNS: `172.16.0.100`
- Mihomo: `172.16.0.101`
- RouterOS DNS server: `172.16.0.100`

A reviewable RouterOS template is provided at
[`routeros/install.rsc`](routeros/install.rsc). Do not import it while another
container still owns `172.16.0.100`.

Mihomo should use GuardDNS's secure listener for real upstream queries:

```yaml
dns:
  enable: true
  listen: :53
  enhanced-mode: fake-ip
  nameserver:
    - 172.16.0.100:5304
  proxy-server-nameserver:
    - https://doh.pub/dns-query
    - 114.114.114.114
```

`proxy-server-nameserver` must remain independent from GuardDNS so Mihomo can
resolve proxy node hostnames during bootstrap. When Mihomo needs a real answer
for an AUTO_FORWARD query, its `nameserver` uses GuardDNS port `5304`; that
listener always uses built-in encrypted DNS and never forwards back to Mihomo,
so the DNS path does not loop.

Add subscription/control-plane domains to `force-secure.txt` and Mihomo's
`fake-ip-filter`, so they always receive real addresses.

## Validation

Local integration tests build a mock Mihomo DNS endpoint and verify:

- mainland domains do not receive fake-IP;
- global domains receive fake-IP only after encrypted validation;
- port 5304 always returns real answers;
- DNSSEC failures remain `SERVFAIL`;
- NXDOMAIN is not converted to fake-IP;
- NODATA is not converted to fake-IP;
- secure real-IP mode and both UDP/TCP listeners start correctly;
- IPv6 policy and environment input validation.
- the functional secure-DNS health check and Prometheus listener metrics.
- AUTO_FORWARD custom ports, seamless failure fallback, exponential backoff,
  and half-open recovery.
- independent MosDNS/Unbound crash recovery and restart metrics.

Run:

```sh
docker build -t guarddns:test .
sh tests/integration.sh guarddns:test
```

GitHub Actions runs the same test before publishing multi-architecture
`linux/amd64`, `linux/arm64`, and `linux/arm/v7` images to GHCR. Scheduled
builds also pick up patched Alpine packages while rule snapshots remain pinned
to reviewable upstream release/commit identifiers.

For restricted build networks, rule release assets may be placed in the ignored
`.test-assets` directory. The Dockerfile still verifies their upstream
checksums. MosDNS and the GuardDNS supervisor are reproducibly built from the
pinned `go.mod`/`go.sum` module graph.
