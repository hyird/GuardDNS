# Review addresses and paths before importing.
# GuardDNS replaces the existing DNS container at 172.16.0.100.

/interface/veth/add name=veth-guarddns address=172.16.0.100/16 gateway=172.16.0.1
/interface/bridge/port/add bridge=br-container interface=veth-guarddns

/container/envs/add list=guarddns key=AUTO_FORWARD value=172.16.0.101
/container/envs/add list=guarddns key=LOG_LEVEL value=warn

/container/mounts/add list=guarddns src=/container/guarddns/data dst=/data

/container/add name=guarddns \
  remote-image=ghcr.io/hyird/guarddns:latest \
  interface=veth-guarddns \
  root-dir=/container/guarddns/root \
  envlists=guarddns \
  mountlists=guarddns \
  start-on-boot=yes \
  logging=yes

# Start the container only after the image extraction has completed.
# /container/start [find where name=guarddns]
# /ip/dns/set servers=172.16.0.100 allow-remote-requests=yes
# Prometheus metrics: http://172.16.0.100:9091/metrics
# Restrict access to port 9091 to trusted monitoring hosts.

# Defense in depth: never expose the RouterOS recursive resolver on WAN.
/ip/firewall/filter/add chain=input in-interface-list=WAN protocol=udp dst-port=53 action=drop comment="guarddns: block public UDP DNS"
/ip/firewall/filter/add chain=input in-interface-list=WAN protocol=tcp dst-port=53 action=drop comment="guarddns: block public TCP DNS"
