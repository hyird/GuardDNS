# GuardDNS

GuardDNS is a fail-closed anti-pollution split DNS container for RouterOS,
Mihomo, and ordinary Docker hosts. It is designed as a smaller, auditable
successor to monolithic DNS bundles.

It combines:

- MosDNS 5.3.4 for routing, validation gates, and isolated caches.
- Unbound 1.25.1 for local recursive DNS with DNSSEC validation.
- Cloudflare and Google DoH with fixed dial IPs for encrypted global answers.
- Optional SOCKS5 transport through Mihomo.
- Optional validate-before-fake-IP integration with Mihomo.

## Why it is stricter

| Property | GuardDNS behavior |
| --- | --- |
| Unknown domains | Encrypted path by default; no plaintext probing |
| Global DNS | DoH only; no AliDNS and no plaintext `8.8.8.8:53` |
| Mainland DNS | Unbound recursion accepted only when A/AAAA answers contain CN IPs; otherwise encrypted fallback |
| Fake-IP | A/AAAA existence is checked through encrypted DNS before asking Mihomo for fake-IP |
| DNSSEC failure | Preserved as `SERVFAIL`, never converted to fake-IP |
| Cache | Separate main/secure memory caches; stale serving disabled |
| Bootstrap | DoH endpoints use fixed dial IPs, so no bootstrap resolver is needed |
| Runtime state | No Redis and no runtime mutation of upstream rule files |
| Supply chain | Pinned MosDNS checksums, verified rule checksums, CI SBOM and provenance |

The routing policy is intentionally fail-closed:

```text
LAN -> RouterOS -> GuardDNS :53
                     |
                     +-- known CN -> Unbound DNSSEC recursion
                     |                 \-- non-CN or failure -> encrypted DoH
                     |
                     +-- known global / unknown -> encrypted DoH
                                                   \-- optional validated Mihomo fake-IP

Mihomo real DNS upstream -> GuardDNS :5304 -> encrypted DoH only
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
  ghcr.io/hyird/guarddns:latest
```

Mihomo fake-IP mode:

```sh
docker run -d \
  --name guarddns \
  --restart unless-stopped \
  -v ./data:/data \
  -e SOCKS5_ADDR=172.16.0.101:7897 \
  -e MIHOMO_DNS_ADDR=172.16.0.101:53 \
  -e IPV6_MODE=off \
  -p 53:53/udp -p 53:53/tcp \
  -p 5304:5304/udp -p 5304:5304/tcp \
  ghcr.io/hyird/guarddns:latest
```

Port `53` provides split DNS and optional fake-IP. Port `5304` always returns a
real answer through encrypted DNS, making it safe as Mihomo's upstream.

## Environment variables

| Variable | Default | Description |
| --- | --- | --- |
| `LOG_LEVEL` | `warn` | `debug`, `info`, `warn`, or `error` |
| `LISTEN_ADDR` | `0.0.0.0:53` | Main UDP/TCP listener |
| `SECURE_LISTEN_ADDR` | `0.0.0.0:5304` | Real-answer encrypted listener |
| `SOCKS5_ADDR` | empty | Optional `host:port` used by both DoH upstreams |
| `MIHOMO_DNS_ADDR` | empty | Optional Mihomo DNS `host:port`; enables validated fake-IP |
| `IPV6_MODE` | `off` | `off` returns empty AAAA; `on` enables IPv6 |
| `CACHE_SIZE` | `16384` | Main cache entries; secure cache uses half |
| `FAST_FALLBACK_MS` | `350` | CN recursion fallback threshold |

Endpoint variables are validated before configuration rendering. Shell
metacharacters and malformed values are rejected.

## Custom rules

The `/data` volume is initialized with:

- `force-secure.txt`: always return encrypted real IP, bypassing fake-IP.
- `force-fakeip.txt`: force the global/fake-IP path.
- `force-direct.txt`: force local Unbound recursion without geographic filtering.

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
resolve proxy node hostnames during bootstrap. GuardDNS's DoH traffic can then
travel through Mihomo SOCKS5 without creating a DNS loop.

Add subscription/control-plane domains to `force-secure.txt` and Mihomo's
`fake-ip-filter`, so they always receive real addresses.

## Validation

Local integration tests build a mock Mihomo DNS and verify:

- mainland domains do not receive fake-IP;
- global domains receive fake-IP only after encrypted validation;
- port 5304 always returns real answers;
- DNSSEC failures remain `SERVFAIL`;
- NXDOMAIN is not converted to fake-IP;
- NODATA is not converted to fake-IP;
- secure real-IP mode and both UDP/TCP listeners start correctly;
- IPv6 policy and environment input validation.

Run:

```sh
docker build -t guarddns:test .
sh tests/integration.sh guarddns:test
```

GitHub Actions runs the same test before publishing multi-architecture
`linux/amd64`, `linux/arm64`, and `linux/arm/v7` images to GHCR. Scheduled
builds also pick up patched Alpine packages while rule snapshots remain pinned
to reviewable upstream release/commit identifiers.

For restricted build networks, release assets may be placed in the ignored
`.test-assets` directory. The Dockerfile uses matching vendored files when
present and still verifies their upstream checksums. Normal CI builds leave
that directory empty and download directly from the official releases.
