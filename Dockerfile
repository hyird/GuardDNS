# syntax=docker/dockerfile:1.7

ARG ALPINE_VERSION=3.24
ARG ALPINE_MIRROR=

FROM alpine:${ALPINE_VERSION} AS mosdns-downloader

ARG MOSDNS_VERSION=5.3.4
ARG TARGETARCH
ARG TARGETVARIANT
ARG ALPINE_MIRROR

RUN if [ -n "$ALPINE_MIRROR" ]; then \
      sed -i "s|https://dl-cdn.alpinelinux.org/alpine|$ALPINE_MIRROR|g" /etc/apk/repositories; \
    fi \
    && apk add --no-cache ca-certificates curl unzip

COPY .test-assets/ /vendor/

RUN set -eux; \
    target_arch="${TARGETARCH:-amd64}"; \
    target_variant="${TARGETVARIANT:-}"; \
    case "${target_arch}/${target_variant}" in \
      amd64/) \
        asset="mosdns-linux-amd64.zip"; \
        checksum="3abcc73080789eb1ccca78dab5049b85ac1e9b8f865ab60158a527b77cd72e85" \
        ;; \
      arm64/) \
        asset="mosdns-linux-arm64.zip"; \
        checksum="82d80a1a21606fca0bc6b65ac6f90d30cff6bb4a19a6ab6a246cf247dbb78bc0" \
        ;; \
      arm/v7) \
        asset="mosdns-linux-arm-7.zip"; \
        checksum="90c9657c572f4424dba4eaf8cf24a5a8d7b6cde96b71657ef7c93045dfef3ce3" \
        ;; \
      *) \
        echo "Unsupported target: ${target_arch}/${target_variant}" >&2; \
        exit 1 \
        ;; \
    esac; \
    if [ -s "/vendor/${asset}" ]; then \
      cp "/vendor/${asset}" /tmp/mosdns.zip; \
    else \
      curl -fsSL --retry 5 \
        "https://github.com/IrineSistiana/mosdns/releases/download/v${MOSDNS_VERSION}/${asset}" \
        -o /tmp/mosdns.zip; \
    fi; \
    echo "${checksum}  /tmp/mosdns.zip" | sha256sum -c -; \
    mkdir -p /out; \
    unzip -j /tmp/mosdns.zip mosdns -d /out; \
    chmod 0755 /out/mosdns

FROM alpine:${ALPINE_VERSION} AS rules-downloader

ARG ALPINE_MIRROR
ARG RULES_VERSION=202607232250
ARG RULES_SHA256=831c3105a7cf3fc7b03f155769a4a9d1627f05ebbda00ba0da15883540a16379
ARG CLASH_RULES_COMMIT=887d83dd2e6e261c8ee660842639839e52b3ccaa
ARG CNCIDR_SHA256=2971f17f525852241aa4d9e104c8cb414e23e2ac81ec01a7118e12ed5048cd13

RUN if [ -n "$ALPINE_MIRROR" ]; then \
      sed -i "s|https://dl-cdn.alpinelinux.org/alpine|$ALPINE_MIRROR|g" /etc/apk/repositories; \
    fi \
    && apk add --no-cache ca-certificates curl unzip

COPY .test-assets/ /vendor/

RUN set -eux; \
    mkdir -p /out /tmp/rules; \
    if [ -s /vendor/rules.zip ]; then \
      cp /vendor/rules.zip /tmp/rules.zip; \
    else \
      curl -fsSL --retry 5 \
        "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/download/${RULES_VERSION}/rules.zip" \
        -o /tmp/rules.zip; \
    fi; \
    echo "${RULES_SHA256}  /tmp/rules.zip" | sha256sum -c -; \
    unzip -j /tmp/rules.zip direct-list.txt proxy-list.txt -d /tmp/rules; \
    cp /tmp/rules/direct-list.txt /out/direct.txt; \
    cp /tmp/rules/proxy-list.txt /out/proxy.txt; \
    if [ -s /vendor/cncidr.txt ]; then \
      cp /vendor/cncidr.txt /tmp/cncidr.yaml; \
    else \
      curl -fsSL --retry 5 \
        "https://raw.githubusercontent.com/Loyalsoldier/clash-rules/${CLASH_RULES_COMMIT}/cncidr.txt" \
        -o /tmp/cncidr.yaml; \
    fi; \
    echo "${CNCIDR_SHA256}  /tmp/cncidr.yaml" | sha256sum -c -; \
    sed \
      -e '/^payload:/d' \
      -e 's/^[[:space:]]*-[[:space:]]*//' \
      -e "s/^'//" \
      -e "s/'[[:space:]]*$//" \
      -e 's/\r$//' \
      /tmp/cncidr.yaml > /out/cncidr.txt; \
    test -s /out/direct.txt; \
    test -s /out/proxy.txt; \
    test -s /out/cncidr.txt

FROM alpine:${ALPINE_VERSION} AS unbound-builder

ARG ALPINE_MIRROR
ARG UNBOUND_VERSION=1.25.2
ARG UNBOUND_SHA256=0d92275c703d5f5f8baba3dab22117dd8c29b495588a5c229768ed6581566600

RUN if [ -n "$ALPINE_MIRROR" ]; then \
      sed -i "s|https://dl-cdn.alpinelinux.org/alpine|$ALPINE_MIRROR|g" /etc/apk/repositories; \
    fi \
    && apk add --no-cache \
      build-base \
      ca-certificates \
      curl \
      expat-dev \
      libevent-dev \
      openssl-dev

COPY .test-assets/ /vendor/

RUN set -eux; \
    if [ -s /vendor/unbound.tar.gz ]; then \
      cp /vendor/unbound.tar.gz /tmp/unbound.tar.gz; \
    else \
      curl -fsSL --retry 5 \
        "https://nlnetlabs.nl/downloads/unbound/unbound-${UNBOUND_VERSION}.tar.gz" \
        -o /tmp/unbound.tar.gz; \
    fi; \
    echo "${UNBOUND_SHA256}  /tmp/unbound.tar.gz" | sha256sum -c -; \
    mkdir -p /src; \
    tar -xzf /tmp/unbound.tar.gz -C /src --strip-components=1; \
    cd /src; \
    ./configure \
      --prefix=/usr \
      --sysconfdir=/etc/unbound \
      --localstatedir=/var \
      --with-conf-file=/etc/unbound/unbound.conf \
      --with-run-dir=/run/unbound \
      --with-pidfile=/run/unbound/unbound.pid \
      --with-rootkey-file=/usr/share/dnssec-root/trusted-key.key \
      --with-username=unbound \
      --with-libevent \
      --with-libexpat=/usr \
      --with-ssl \
      --with-pthreads \
      --without-pythonmodule \
      --without-pyunbound \
      --disable-flto \
      --disable-sha1 \
      --disable-dsa; \
    make -j"$(getconf _NPROCESSORS_ONLN)"; \
    make DESTDIR=/out install

FROM alpine:${ALPINE_VERSION}

ARG UNBOUND_VERSION=1.25.2
ARG MOSDNS_VERSION=5.3.4
ARG ALPINE_MIRROR

LABEL org.opencontainers.image.title="GuardDNS" \
      org.opencontainers.image.description="Fail-closed anti-pollution split DNS for RouterOS and Mihomo" \
      org.opencontainers.image.source="https://github.com/hyird/GuardDNS" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${MOSDNS_VERSION}"

RUN if [ -n "$ALPINE_MIRROR" ]; then \
      sed -i "s|https://dl-cdn.alpinelinux.org/alpine|$ALPINE_MIRROR|g" /etc/apk/repositories; \
    fi \
    && apk add --no-cache \
      ca-certificates \
      dnssec-root \
      expat \
      libevent \
      libssl3 \
      tini \
      tzdata \
    && addgroup -S unbound \
    && adduser -S -D -H -h /etc/unbound -s /sbin/nologin -G unbound unbound \
    && mkdir -p /etc/guarddns /usr/share/guarddns/rules /run/guarddns/unbound /data

COPY --from=mosdns-downloader /out/mosdns /usr/local/bin/mosdns
COPY --from=rules-downloader /out/ /usr/share/guarddns/rules/
COPY --from=unbound-builder /out/usr/sbin/unbound /usr/sbin/unbound
COPY --from=unbound-builder /out/usr/sbin/unbound-checkconf /usr/sbin/unbound-checkconf
COPY config/ /etc/guarddns/
COPY scripts/entrypoint.sh /usr/local/bin/guarddns-entrypoint

RUN chmod 0755 /usr/local/bin/guarddns-entrypoint \
    && unbound -V | grep -F "Version ${UNBOUND_VERSION}" \
    && cp /usr/share/dnssec-root/trusted-key.key /run/guarddns/unbound/root.key \
    && chown -R unbound:unbound /run/guarddns/unbound \
    && sed 's/__UNBOUND_IPV6__/no/g' /etc/guarddns/unbound.conf.tmpl > /tmp/unbound.conf \
    && unbound-checkconf /tmp/unbound.conf >/dev/null \
    && rm -f /tmp/unbound.conf /run/guarddns/unbound/root.key

ENV TZ=Asia/Shanghai \
    LOG_LEVEL=warn \
    LISTEN_ADDR=0.0.0.0:53 \
    SECURE_LISTEN_ADDR=0.0.0.0:5304 \
    SOCKS5_ADDR= \
    MIHOMO_DNS_ADDR= \
    IPV6_MODE=off \
    CACHE_SIZE=16384 \
    FAST_FALLBACK_MS=350

VOLUME ["/data"]
EXPOSE 53/udp 53/tcp 5304/udp 5304/tcp

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/guarddns-entrypoint"]
