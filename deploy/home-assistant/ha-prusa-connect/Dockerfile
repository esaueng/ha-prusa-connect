FROM debian:trixie-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258

RUN groupadd --gid 10001 prusa \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin prusa \
    && install -d -m 0755 /etc/ssl/certs /usr/local/share/ha-prusa-connect

LABEL org.opencontainers.image.source="https://github.com/esaueng/ha-prusa-connect"
COPY --chmod=0755 rootfs/prusa_connect /prusa_connect
COPY --chmod=0644 rootfs/etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chmod=0644 server.yaml /usr/local/share/ha-prusa-connect/server.yaml
COPY --chmod=0755 run.sh /usr/local/bin/ha-prusa-connect

ENTRYPOINT ["/usr/local/bin/ha-prusa-connect"]
