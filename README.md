# GuardDNS

GuardDNS is a fail-closed anti-pollution split DNS container for RouterOS,
Mihomo, and ordinary Docker hosts. It is designed as a smaller, auditable
successor to monolithic DNS bundles.

It combines:

- MosDNS 5.3.4 for routing, validation gates, and isolated caches.
- Alpine-packaged Unbound 1.25.1 for encrypted real-IP DNS with local DNSSEC
  validation and caching.
- An in-process loopback bridge to AliDNS and DNSPod DoH over standard HTTPS
  port 443, reachable without special RouterOS routes.
- Optional Mihomo DNS integration for validated fake-IP.
- Functional Docker health checks and Prometheus runtime metrics.

## Why it is stricter

| Property | GuardDNS behavior |
| --- | --- |
| Unknown domains | Encrypted validation first; valid A answers use Mihomo DNS when AUTO_FORWARD is enabled |
| Global DNS | Mihomo DNS when AUTO_FORWARD is enabled; built-in encrypted real IP otherwise and during outage |
| Mainland DNS | Unbound answers are accepted only when A records contain CN IPs; non-CN answers enter the global path |
| Fake-IP | A-record existence is checked through encrypted DNS before asking Mihomo for fake-IP |
| DNSSEC failure | Preserved as `SERVFAIL`, never converted to fake-IP |
| Cache | Separate CN/secure-real caches; fake-IP is never cached by GuardDNS |
| Encrypted upstream | The Go bridge transports Unbound queries over DoH/443; Unbound validates DNSSEC locally |
| Runtime state | No Redis and no runtime mutation of upstream rule files |
| Supply chain | Pinned Go module graph, verified rule checksums, CI SBOM and provenance |

The routing policy is intentionally fail-closed:

```text
LAN -> RouterOS -> GuardDNS :53
                     |
                     +-- known CN -> validating Unbound -> loopback DoH bridge
                     |                 \-- non-CN or failure -> encrypted real IP
                     |
                     +-- known global / unknown -> encrypted real IP
                                                    \-- optional validated Mihomo fake-IP

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
  -p 127.0.0.1:9091:9091/tcp \
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
  -p 127.0.0.1:9091:9091/tcp \
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
Listeners are fixed at `0.0.0.0:53`, `0.0.0.0:5304`, and `0.0.0.0:9091`;
the timezone is `Asia/Shanghai`; CN/secure cache sizes are `16384`/`8192`;
Known-CN lookups wait for their real response before the returned IP is
classified. This avoids racing a healthy but slower CN response against
Mihomo's fake-IP response.

`AUTO_FORWARD` accepts `no`, a host/IPv4 address, or `host:port`. The DNS port
defaults to `53` when omitted. Setting an address sends validated non-CN A
queries to that Mihomo DNS service for fake-IP; SOCKS5 is not used. GuardDNS
first verifies the domain with its built-in encrypted real-IP resolver. If
Mihomo DNS fails, it reuses that same real answer without another lookup.
Failures use exponential retry delays from `1s` to `5min`; while the circuit is
open, queries bypass Mihomo immediately. A half-open probe restores forwarding
automatically after recovery. Set it to `no` to use encrypted real IP only.
The built-in real-IP path uses two independent providers over authenticated
DNS-over-HTTPS. The Go process transports DNS wire messages between the
loopback-only Unbound forwarder and those providers; Unbound validates DNSSEC
locally before MosDNS may pass an A query to Mihomo.

Logs always go to the container's standard output/error stream and are never
written to a fixed file. `LOG_LEVEL` controls supervisor, MosDNS, and Unbound
verbosity through the same setting.

## Health and metrics

The Go entrypoint independently supervises MosDNS and Unbound. If either child
exits, it is restarted with jittered exponential delays from `1s` to `30s`;
the container stays up and the other resolver continues serving what it can.
The health check verifies `/plugins/guarddns/healthz` and performs a real A
query through the secure `127.0.0.1:5304` listener. A restarting Unbound may
report `degraded` but stays healthy while encrypted DNS still works; missing
MosDNS, stale supervisor state, or an unusable secure DNS path reports
unhealthy.

Supervisor state is sent to MosDNS through the Unix datagram socket
`/run/guarddns/supervisor.sock`. This does not create another TCP/UDP listener.
Private DNS data paths are loopback-only: MosDNS to Unbound on
`127.0.0.1:5335`, and Unbound to the in-process DoH bridge on
`127.0.0.1:5336`. Neither is exposed by the image.

Prometheus metrics are available at `/metrics` on the fixed container listener
`0.0.0.0:9091`. The Docker examples expose it only on host loopback:

```text
http://127.0.0.1:9091/metrics
```

In addition to Go, process, cache, and tagged upstream metrics, GuardDNS exports
end-to-end counters and latency histograms with the collector names `main` and
`secure`. MosDNS upstream metrics distinguish `unbound`, `unbound_secure`, and
`auto_forward`. Supervisor metrics
begin with `mosdns_guarddns_component_`; circuit state, retry delay, failures,
and bypasses begin with `mosdns_guarddns_circuit_`.

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
