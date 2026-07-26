# Third-party components

The GuardDNS image contains third-party programs, libraries, and data:

- [MosDNS](https://github.com/IrineSistiana/mosdns) v5.3.4, GPL-3.0. GuardDNS
  builds a custom MosDNS entry binary that registers its supervision and
  circuit-breaker plugins.
- [Unbound](https://github.com/NLnetLabs/unbound), BSD-3-Clause.
- [Loyalsoldier v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat),
  GPL-3.0.
- [Loyalsoldier clash-rules](https://github.com/Loyalsoldier/clash-rules),
  GPL-3.0.
- Alpine Linux packages, under their respective licenses.

The MosDNS source version and complete Go dependency graph are pinned by
`go.mod`/`go.sum`. Domain rules are verified against the upstream release
checksum; the CN CIDR snapshot is pinned by commit and verified against its
recorded checksum.
