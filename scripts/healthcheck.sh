#!/bin/sh
set -eu

wget -q -T 3 -O - "http://127.0.0.1:9091/plugins/guarddns/healthz" \
  | grep -Eq '^(ok|degraded)'
