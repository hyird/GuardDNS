#!/bin/sh
set -eu

image=${1:-guarddns:test}
alpine_mirror=${TEST_ALPINE_MIRROR:-}
network="guarddns-test-$$"
mock_name="guarddns-mock-$$"
dns_name="guarddns-under-test-$$"
client_name="guarddns-client-$$"

cleanup() {
  docker rm -f "$client_name" "$dns_name" "$mock_name" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  docker inspect -f '{{json .State}}' "$dns_name" >&2 2>/dev/null || true
  docker logs "$dns_name" >&2 || true
  docker exec "$dns_name" ps -ef >&2 2>/dev/null || true
  docker exec "$dns_name" /usr/local/bin/guarddns-healthcheck >&2 2>/dev/null || true
  if [ -n "${dns_ip:-}" ]; then
    docker exec "$client_name" \
      dig +time=3 +tries=1 "@$dns_ip" dns.google A >&2 2>/dev/null || true
  fi
  exit 1
}

docker network create "$network" >/dev/null

docker image inspect -f '{{json .Config.Healthcheck.Test}}' "$image" \
  | grep -q 'guarddns-healthcheck' \
  || fail "image does not define the GuardDNS health check"

# Model a Mihomo DNS service on a custom port.
docker run -d \
  --name "$mock_name" \
  --network "$network" \
  -e "ALPINE_MIRROR=$alpine_mirror" \
  alpine:3.24 \
  sh -c 'set -eu
    if [ -n "$ALPINE_MIRROR" ]; then
      sed -i "s|https://dl-cdn.alpinelinux.org/alpine|$ALPINE_MIRROR|g" /etc/apk/repositories
    fi
    apk add --no-cache dnsmasq >/dev/null
    exec dnsmasq --keep-in-foreground --no-resolv --no-hosts \
      --port=5353 --log-facility=- --address=/#/198.18.0.42' >/dev/null

mock_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$mock_name")

docker run -d \
  --name "$dns_name" \
  --network "$network" \
  -e "AUTO_FORWARD=$mock_ip:5353" \
  -e LOG_LEVEL=info \
  "$image" >/dev/null

dns_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$dns_name")

docker run -d \
  --name "$client_name" \
  --network "$network" \
  -e "ALPINE_MIRROR=$alpine_mirror" \
  alpine:3.24 \
  sh -c 'if [ -n "$ALPINE_MIRROR" ]; then
      sed -i "s|https://dl-cdn.alpinelinux.org/alpine|$ALPINE_MIRROR|g" /etc/apk/repositories
    fi
    apk add --no-cache bind-tools >/dev/null && sleep 3600' >/dev/null

ready=0
i=0
while [ "$i" -lt 40 ]; do
  if docker exec "$client_name" dig +time=1 +tries=1 +short "@$dns_ip" dns.google A \
      | grep -Eq '^[0-9]+\.'; then
    ready=1
    break
  fi
  i=$((i + 1))
  sleep 1
done
[ "$ready" -eq 1 ] || fail "GuardDNS did not become ready"

# Use AliDNS's own stable mainland A records. www.baidu.com is geo-sensitive
# and can legitimately return a Hong Kong address to an overseas CI runner,
# which GuardDNS must classify as NON-CN.
cn_answer=$(docker exec "$client_name" dig +time=3 +tries=1 +short "@$dns_ip" dns.alidns.com A)
[ -n "$cn_answer" ] || fail "mainland domain returned no A record"
printf '%s\n' "$cn_answer" | grep -q '198\.18\.0\.42' \
  && fail "mainland domain was incorrectly sent to fake-IP"

global_answer=$(docker exec "$client_name" dig +time=3 +tries=1 +short "@$dns_ip" www.google.com A)
printf '%s\n' "$global_answer" | grep -qx '198.18.0.42' \
  || fail "global domain did not use validated Mihomo fake-IP"

control_answer=$(docker exec "$client_name" dig +time=3 +tries=1 +short "@$dns_ip" dns.google A)
[ -n "$control_answer" ] || fail "force-secure domain returned no A record"
printf '%s\n' "$control_answer" | grep -q '198\.18\.0\.42' \
  && fail "force-secure domain leaked into fake-IP mode"

secure_answer=$(docker exec "$client_name" dig +time=3 +tries=1 +short -p 5304 "@$dns_ip" www.google.com A)
[ -n "$secure_answer" ] || fail "secure listener returned no A record"
printf '%s\n' "$secure_answer" | grep -q '198\.18\.0\.42' \
  && fail "secure listener returned fake-IP"

dnssec_status=$(docker exec "$client_name" dig +time=4 +tries=1 "@$dns_ip" dnssec-failed.org A +noall +comments || true)
printf '%s\n' "$dnssec_status" | grep -q 'status: SERVFAIL' \
  || fail "DNSSEC failure was not preserved as SERVFAIL: $dnssec_status"
printf '%s\n' "$dnssec_status" | grep -q '198\.18\.0\.42' \
  && fail "DNSSEC failure was converted to fake-IP"

nxdomain_name="missing-$$.invalid"
nxdomain_status=$(docker exec "$client_name" dig +time=4 +tries=1 "@$dns_ip" "$nxdomain_name" A +noall +comments || true)
printf '%s\n' "$nxdomain_status" | grep -q 'status: NXDOMAIN' \
  || fail "NXDOMAIN was not preserved: $nxdomain_status"

nodata_status=$(docker exec "$client_name" dig +time=4 +tries=1 "@$dns_ip" _dmarc.example.com A +noall +comments +answer || true)
printf '%s\n' "$nodata_status" | grep -q 'status: NOERROR' \
  || fail "NODATA response did not remain NOERROR: $nodata_status"
printf '%s\n' "$nodata_status" | grep -q '198\.18\.0\.42' \
  && fail "NODATA response was converted to fake-IP"

aaaa_answer=$(docker exec "$client_name" dig +time=3 +tries=1 +short "@$dns_ip" cloudflare.com AAAA)
[ -z "$aaaa_answer" ] || fail "IPv6-disabled resolver returned an AAAA record"
secure_aaaa_answer=$(docker exec "$client_name" \
  dig +time=3 +tries=1 +short -p 5304 "@$dns_ip" cloudflare.com AAAA)
[ -z "$secure_aaaa_answer" ] || fail "secure listener returned an AAAA record"

tcp_answer=$(docker exec "$client_name" dig +tcp +time=3 +tries=1 +short "@$dns_ip" dns.google A)
[ -n "$tcp_answer" ] || fail "TCP listener returned no A record"

docker exec "$dns_name" /usr/local/bin/guarddns-healthcheck \
  || fail "container health check failed"

metrics=$(docker exec "$dns_name" \
  wget -q -T 3 -O - http://127.0.0.1:9091/metrics)
printf '%s\n' "$metrics" | grep -q 'mosdns_metrics_collector_query_total{name="main"}' \
  || fail "main listener metrics were not exported"
printf '%s\n' "$metrics" | grep -q 'mosdns_metrics_collector_query_total{name="secure"}' \
  || fail "secure listener metrics were not exported"
printf '%s\n' "$metrics" | grep -q 'mosdns_guarddns_component_up{component="unbound"} 1' \
  || fail "Unbound supervisor state was not exported"
printf '%s\n' "$metrics" | grep -q 'mosdns_guarddns_component_up{component="doh_bridge"} 1' \
  || fail "encrypted bridge state was not exported"
printf '%s\n' "$metrics" | grep -q 'mosdns_guarddns_circuit_state{name="auto_forward_circuit"}' \
  || fail "AUTO_FORWARD circuit state was not exported"

docker exec "$dns_name" sh -c \
  "grep -q 'forward-addr: 127.0.0.1@5336' /run/guarddns/unbound.conf &&
   grep -q 'do-not-query-localhost: no' /run/guarddns/unbound.conf &&
   ! grep -R -E 'addr: https://|dial_addr:|forward-addr: (223\\.5\\.5\\.5|119\\.29\\.29\\.29)' /run/guarddns /etc/guarddns"

# Stop the configured Mihomo DNS. Queries must seamlessly reuse their validated
# real responses; two consecutive failures open the exponential-backoff
# circuit.
docker stop "$mock_name" >/dev/null
failover_answer=$(docker exec "$client_name" \
  dig +time=3 +tries=1 +short "@$dns_ip" www.youtube.com A)
[ -n "$failover_answer" ] || fail "AUTO_FORWARD failure returned no real answer"
printf '%s\n' "$failover_answer" | grep -q '198\.18\.0\.42' \
  && fail "AUTO_FORWARD failure returned fake-IP"
second_failover_answer=$(docker exec "$client_name" \
  dig +time=3 +tries=1 +short "@$dns_ip" github.com A)
[ -n "$second_failover_answer" ] || fail "second AUTO_FORWARD failure returned no real answer"
printf '%s\n' "$second_failover_answer" | grep -q '198\.18\.0\.42' \
  && fail "second AUTO_FORWARD failure returned fake-IP"
sleep 1
docker logs "$dns_name" 2>&1 \
  | grep -q 'AUTO_FORWARD circuit opened' \
  || fail "AUTO_FORWARD circuit did not enter exponential backoff"

# After the backoff expires, one half-open probe should restore forwarding.
docker start "$mock_name" >/dev/null
sleep 2
recovered_answer=$(docker exec "$client_name" \
  dig +time=3 +tries=1 +short "@$dns_ip" www.twitter.com A)
printf '%s\n' "$recovered_answer" | grep -qx '198.18.0.42' \
  || fail "AUTO_FORWARD did not recover after the backoff probe"
docker logs "$dns_name" 2>&1 \
  | grep -q 'AUTO_FORWARD circuit recovered' \
  || fail "AUTO_FORWARD circuit did not report recovery"

# The Go entrypoint must recover each DNS child independently without exiting.
old_unbound_pid=$(docker exec "$dns_name" pidof unbound)
docker exec "$dns_name" kill -9 "$old_unbound_pid"
new_unbound_pid=
i=0
while [ "$i" -lt 20 ]; do
  candidate=$(docker exec "$dns_name" pidof unbound 2>/dev/null || true)
  if [ -n "$candidate" ] && [ "$candidate" != "$old_unbound_pid" ]; then
    new_unbound_pid=$candidate
    break
  fi
  i=$((i + 1))
  sleep 1
done
[ -n "$new_unbound_pid" ] || fail "Unbound was not restarted"
docker inspect -f '{{.State.Running}}' "$dns_name" | grep -qx true \
  || fail "container exited when Unbound crashed"

old_mosdns_pid=$(docker exec "$dns_name" pidof mosdns)
docker exec "$dns_name" kill -9 "$old_mosdns_pid"
new_mosdns_pid=
i=0
while [ "$i" -lt 20 ]; do
  candidate=$(docker exec "$dns_name" pidof mosdns 2>/dev/null || true)
  if [ -n "$candidate" ] && [ "$candidate" != "$old_mosdns_pid" ]; then
    new_mosdns_pid=$candidate
    break
  fi
  i=$((i + 1))
  sleep 1
done
[ -n "$new_mosdns_pid" ] || fail "MosDNS was not restarted"

healthy=0
i=0
while [ "$i" -lt 15 ]; do
  if docker exec "$dns_name" /usr/local/bin/guarddns-healthcheck; then
    healthy=1
    break
  fi
  i=$((i + 1))
  sleep 1
done
[ "$healthy" -eq 1 ] || fail "health endpoint did not recover after MosDNS restart"

metrics=$(docker exec "$dns_name" wget -q -T 3 -O - http://127.0.0.1:9091/metrics)
printf '%s\n' "$metrics" \
  | grep -Eq 'mosdns_guarddns_component_restarts_total\{component="unbound"\} [1-9][0-9]*' \
  || fail "Unbound restart count was not exported"
printf '%s\n' "$metrics" \
  | grep -Eq 'mosdns_guarddns_component_restarts_total\{component="mosdns"\} [1-9][0-9]*' \
  || fail "MosDNS restart count was not exported"

post_restart_answer=$(docker exec "$client_name" \
  dig +time=3 +tries=1 +short "@$dns_ip" www.google.com A)
[ -n "$post_restart_answer" ] || fail "DNS stopped serving after child recovery"

if docker run --rm -e 'AUTO_FORWARD=bad:notaport' "$image" >/dev/null 2>&1; then
  fail "AUTO_FORWARD with a non-numeric port was accepted"
fi

if docker run --rm -e 'AUTO_FORWARD=bad:65536' "$image" >/dev/null 2>&1; then
  fail "AUTO_FORWARD with an out-of-range port was accepted"
fi

if docker run --rm -e 'AUTO_FORWARD=:53' "$image" >/dev/null 2>&1; then
  fail "AUTO_FORWARD with an empty host was accepted"
fi

if docker run --rm -e 'LOG_LEVEL=verbose' "$image" >/dev/null 2>&1; then
  fail "invalid LOG_LEVEL was accepted"
fi

# Recreate the resolver without a Mihomo upstream and verify that the default
# secure real-IP mode starts and never returns the mock fake address.
docker rm -f "$dns_name" >/dev/null
docker run -d \
  --name "$dns_name" \
  --network "$network" \
  "$image" >/dev/null
dns_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$dns_name")

ready=0
i=0
while [ "$i" -lt 40 ]; do
  if docker exec "$client_name" dig +time=1 +tries=1 +short "@$dns_ip" dns.google A \
      | grep -Eq '^[0-9]+\.'; then
    ready=1
    break
  fi
  i=$((i + 1))
  sleep 1
done
[ "$ready" -eq 1 ] || fail "secure real-IP mode did not become ready"

secure_mode_answer=$(docker exec "$client_name" dig +time=3 +tries=1 +short "@$dns_ip" www.google.com A)
[ -n "$secure_mode_answer" ] || fail "secure real-IP mode returned no A record"
printf '%s\n' "$secure_mode_answer" | grep -q '198\.18\.0\.42' \
  && fail "secure real-IP mode returned fake-IP"

printf '%s\n' "ALL TESTS PASSED"
