# Third-party components

The GuardDNS image contains unmodified third-party programs and data:

- [MosDNS](https://github.com/IrineSistiana/mosdns), GPL-3.0.
- [Unbound](https://github.com/NLnetLabs/unbound), BSD-3-Clause.
- [Loyalsoldier v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat),
  GPL-3.0.
- [Loyalsoldier clash-rules](https://github.com/Loyalsoldier/clash-rules),
  GPL-3.0.
- Alpine Linux packages, under their respective licenses.

The Dockerfile records the exact MosDNS release and verifies every downloaded
MosDNS binary. Domain rules are verified against the upstream release checksum;
the CN CIDR snapshot is pinned by commit and verified against its recorded
checksum.
