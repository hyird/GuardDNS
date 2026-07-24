#!/bin/sh
set -eu

image=${1:-guarddns:test}
alpine_mirror=${TEST_ALPINE_MIRROR:-}
test_socks5_addr=${TEST_SOCKS5_ADDR:-}
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
  docker logs "$dns_name" >&2 2>/dev/null || true
  exit 1
}

docker network create "$network" >/dev/null

docker run -d \
  --name "$mock_name" \
  --network "$network" \
  -e "ALPINE_MIRROR=$alpine_mirror" \
  alpine:3.24 \
  sh -c 'if [ -n "$ALPINE_MIRROR" ]; then
      sed -i "s|https://dl-cdn.alpinelinux.org/alpine|$ALPINE_MIRROR|g" /etc/apk/repositories
    fi
    apk add --no-cache dnsmasq >/dev/null &&
    exec dnsmasq --keep-in-foreground --no-resolv --no-hosts \
      --log-facility=- --address=/#/198.18.0.42' >/dev/null

mock_ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$mock_name")

docker run -d \
  --name "$dns_name" \
  --network "$network" \
  -e LOG_LEVEL=info \
  -e IPV6_MODE=off \
  -e "SOCKS5_ADDR=$test_socks5_addr" \
  -e "MIHOMO_DNS_ADDR=${mock_ip}:53" \
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

cn_answer=$(docker exec "$client_name" dig +time=3 +tries=1 +short "@$dns_ip" www.baidu.com A)
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
[ -z "$aaaa_answer" ] || fail "IPV6_MODE=off returned an AAAA record"

tcp_answer=$(docker exec "$client_name" dig +tcp +time=3 +tries=1 +short "@$dns_ip" dns.google A)
[ -n "$tcp_answer" ] || fail "TCP listener returned no A record"

docker exec "$dns_name" sh -c \
  "! grep -R -E '223\\.5\\.5\\.5|223\\.6\\.6\\.6|udp://8\\.8\\.8\\.8' /run/guarddns /etc/guarddns"

if docker run --rm -e 'SOCKS5_ADDR=bad;value' "$image" >/dev/null 2>&1; then
  fail "unsafe endpoint value was accepted"
fi

# Recreate the resolver without a Mihomo upstream and verify that the default
# secure real-IP mode starts and never returns the mock fake address.
docker rm -f "$dns_name" >/dev/null
docker run -d \
  --name "$dns_name" \
  --network "$network" \
  -e LOG_LEVEL=info \
  -e IPV6_MODE=off \
  -e "SOCKS5_ADDR=$test_socks5_addr" \
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
