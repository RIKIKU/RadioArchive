# Recorder client image (SPEC.md R3), amd64 and arm64. Build from the
# repository root:
#
#   docker buildx build --platform linux/amd64,linux/arm64 -f recorder/client/Dockerfile .
#
# The client needs cgo and libopus, so unlike the API (A38) it isn't static.
# The build stage runs on the build machine and cross-compiles for the target
# with Debian's cross toolchain and the target's libopus, so no emulation is
# needed. The final stage is distroless base (glibc) plus libopus.
#
# Run it beside trunk-recorder, sharing its network namespace (the plugin
# sends to 127.0.0.1 only) and with trunk-recorder's media directory mounted
# at the same path. Settings are in cmd/radioarchive-client.
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
ARG TARGETARCH
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) triplet=x86_64-linux-gnu; cc=gcc-x86-64-linux-gnu ;; \
      arm64) triplet=aarch64-linux-gnu; cc=gcc-aarch64-linux-gnu ;; \
      *) echo "unsupported architecture $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    pkgs="pkg-config libopus-dev:$TARGETARCH"; \
    if [ "$TARGETARCH" != "$(dpkg --print-architecture)" ]; then \
      dpkg --add-architecture "$TARGETARCH"; \
      pkgs="$pkgs $cc libc6-dev:$TARGETARCH"; \
    fi; \
    apt-get update; \
    apt-get install -y --no-install-recommends $pkgs; \
    rm -rf /var/lib/apt/lists/*; \
    echo "$triplet" > /triplet
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN set -eu; \
    triplet=$(cat /triplet); \
    CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH CC=$triplet-gcc \
    PKG_CONFIG_LIBDIR=/usr/lib/$triplet/pkgconfig:/usr/share/pkgconfig \
      go build -trimpath -ldflags "-s -w" -o /out/radioarchive-client ./cmd/radioarchive-client; \
    mkdir -p /out/lib; \
    cp -L /usr/lib/$triplet/libopus.so.0 /out/lib/libopus.so.0; \
    mkdir -p /out/queue

FROM gcr.io/distroless/base-debian13:nonroot@sha256:83e67481ae9fc8c7a6d211113431fb8085619bed49672e583261111bac184f0d
# /usr/lib is on the dynamic loader's default path on every architecture.
COPY --from=build /out/lib/libopus.so.0 /usr/lib/libopus.so.0
COPY --from=build /out/radioarchive-client /usr/local/bin/radioarchive-client
# The queue of unsent recordings (SPEC.md R5, QUEUE_DIR). Mount a volume or
# a host directory here, writable by the user the client runs as, so the
# queue survives a restart of the container.
COPY --from=build --chown=65532:65532 /out/queue /var/lib/radioarchive-client/queue
ENTRYPOINT ["/usr/local/bin/radioarchive-client"]
