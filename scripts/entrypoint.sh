#!/bin/sh
set -eu

runtime_dir=/run/guarddns
unbound_runtime_dir=/run/guarddns/unbound
config_dir=/etc/guarddns
data_dir=/data

log() {
  printf '%s %s\n' "[GuardDNS]" "$*"
}

die() {
  log "ERROR: $*"
  exit 1
}

validate_endpoint() {
  name=$1
  value=$2
  [ -n "$value" ] || return 0
  printf '%s' "$value" | grep -Eq '^[A-Za-z0-9._:-]+$' \
    || die "$name contains unsafe characters"
  host=${value%:*}
  port=${value##*:}
  [ -n "$host" ] && [ "$host" != "$value" ] \
    || die "$name must be host:port"
  validate_uint "$name port" "$port"
  [ "$port" -ge 1 ] && [ "$port" -le 65535 ] \
    || die "$name port must be between 1 and 65535"
}

validate_uint() {
  name=$1
  value=$2
  printf '%s' "$value" | grep -Eq '^[0-9]+$' || die "$name must be an integer"
}

LOG_LEVEL=${LOG_LEVEL:-warn}
LISTEN_ADDR=${LISTEN_ADDR:-0.0.0.0:53}
SECURE_LISTEN_ADDR=${SECURE_LISTEN_ADDR:-0.0.0.0:5304}
SOCKS5_ADDR=${SOCKS5_ADDR:-}
MIHOMO_DNS_ADDR=${MIHOMO_DNS_ADDR:-}
IPV6_MODE=${IPV6_MODE:-off}
CACHE_SIZE=${CACHE_SIZE:-16384}
FAST_FALLBACK_MS=${FAST_FALLBACK_MS:-350}

case "$LOG_LEVEL" in
  debug|info|warn|error) ;;
  *) die "LOG_LEVEL must be debug, info, warn, or error" ;;
esac

case "$IPV6_MODE" in
  off)
    ipv6_block_match='qtype 28'
    unbound_ipv6=no
    ;;
  on)
    ipv6_block_match='_false'
    unbound_ipv6=yes
    ;;
  *) die "IPV6_MODE must be off or on" ;;
esac

validate_endpoint LISTEN_ADDR "$LISTEN_ADDR"
validate_endpoint SECURE_LISTEN_ADDR "$SECURE_LISTEN_ADDR"
validate_endpoint SOCKS5_ADDR "$SOCKS5_ADDR"
validate_endpoint MIHOMO_DNS_ADDR "$MIHOMO_DNS_ADDR"
validate_uint CACHE_SIZE "$CACHE_SIZE"
validate_uint FAST_FALLBACK_MS "$FAST_FALLBACK_MS"

[ "$CACHE_SIZE" -ge 2 ] && [ "$CACHE_SIZE" -le 1048576 ] \
  || die "CACHE_SIZE must be between 2 and 1048576"
[ "$FAST_FALLBACK_MS" -ge 1 ] && [ "$FAST_FALLBACK_MS" -le 5000 ] \
  || die "FAST_FALLBACK_MS must be between 1 and 5000"
secure_cache_size=$((CACHE_SIZE / 2))

mkdir -p "$runtime_dir" "$unbound_runtime_dir" "$data_dir"

for name in force-secure.txt force-fakeip.txt force-direct.txt; do
  if [ ! -e "$data_dir/$name" ]; then
    cp "$config_dir/defaults/$name" "$data_dir/$name"
  fi
done

root_key_source=
for candidate in \
  /usr/share/dnssec-root/trusted-key.key \
  /usr/share/dnssec-root/root.key \
  /etc/unbound/root.key; do
  if [ -s "$candidate" ]; then
    root_key_source=$candidate
    break
  fi
done
[ -n "$root_key_source" ] || die "DNSSEC root trust anchor was not found"
cp "$root_key_source" "$unbound_runtime_dir/root.key"
chown -R unbound:unbound "$unbound_runtime_dir"
chmod 0644 "$unbound_runtime_dir/root.key"

sed \
  -e "s|__UNBOUND_IPV6__|$unbound_ipv6|g" \
  "$config_dir/unbound.conf.tmpl" > "$runtime_dir/unbound.conf"

socks_line=
if [ -n "$SOCKS5_ADDR" ]; then
  socks_line="      socks5: '$SOCKS5_ADDR'"
fi

sed \
  -e "s|__LOG_LEVEL__|$LOG_LEVEL|g" \
  -e "s|__LISTEN_ADDR__|'$LISTEN_ADDR'|g" \
  -e "s|__SECURE_LISTEN_ADDR__|'$SECURE_LISTEN_ADDR'|g" \
  -e "s|__CACHE_SIZE__|$CACHE_SIZE|g" \
  -e "s|__SECURE_CACHE_SIZE__|$secure_cache_size|g" \
  -e "s|__FAST_FALLBACK_MS__|$FAST_FALLBACK_MS|g" \
  -e "s|__IPV6_BLOCK_MATCH__|$ipv6_block_match|g" \
  "$config_dir/mosdns.yaml.tmpl" > "$runtime_dir/mosdns.yaml"

if [ -n "$MIHOMO_DNS_ADDR" ]; then
  sed \
    -e "s|__MIHOMO_DNS_ADDR__|'$MIHOMO_DNS_ADDR'|g" \
    -e "s|__SECURE_CACHE_SIZE__|$secure_cache_size|g" \
    -e "s|__SOCKS5_LINE__|$socks_line|g" \
    "$config_dir/foreign-mihomo.yaml.tmpl" > "$runtime_dir/foreign.yaml"
  mode=mihomo
else
  sed \
    -e "s|__SECURE_CACHE_SIZE__|$secure_cache_size|g" \
    -e "s|__SOCKS5_LINE__|$socks_line|g" \
    "$config_dir/foreign-secure.yaml" > "$runtime_dir/foreign.yaml"
  mode=secure
fi

unbound-checkconf "$runtime_dir/unbound.conf" >/dev/null

unbound -d -c "$runtime_dir/unbound.conf" &
unbound_pid=$!

mosdns start -c "$runtime_dir/mosdns.yaml" &
mosdns_pid=$!

terminate() {
  trap - TERM INT EXIT
  kill "$mosdns_pid" "$unbound_pid" 2>/dev/null || true
  wait "$mosdns_pid" "$unbound_pid" 2>/dev/null || true
}
trap terminate TERM INT EXIT

log "ready mode=$mode listen=$LISTEN_ADDR secure=$SECURE_LISTEN_ADDR ipv6=$IPV6_MODE"

while :; do
  if ! kill -0 "$unbound_pid" 2>/dev/null; then
    wait "$unbound_pid" || status=$?
    die "Unbound exited with status ${status:-0}"
  fi
  if ! kill -0 "$mosdns_pid" 2>/dev/null; then
    wait "$mosdns_pid" || status=$?
    die "MosDNS exited with status ${status:-0}"
  fi
  sleep 1
done
