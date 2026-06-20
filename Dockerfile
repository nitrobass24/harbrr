# syntax=docker/dockerfile:1
#
# Multi-stage build for harbrr. The binary is pure-Go (CGO_ENABLED=0), so the
# final image is a tiny non-root Alpine with just the static binary.

# The build stage stays on the native runner platform (BUILDPLATFORM) and Go
# cross-compiles to the requested target (TARGETOS/TARGETARCH, injected by buildx),
# so a multi-arch build never pays for an emulated Go toolchain. CGO is off, so this
# is a pure cross-compile. For a plain `docker build` the target args are empty and
# Go builds for the host.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=docker
ARG COMMIT=none
ARG DATE=unknown
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w \
      -X github.com/autobrr/harbrr/internal/version.Version=${VERSION} \
      -X github.com/autobrr/harbrr/internal/version.Commit=${COMMIT} \
      -X github.com/autobrr/harbrr/internal/version.Date=${DATE}" \
    -o /out/harbrr ./cmd/harbrr

# Resolve ca-certificates + tzdata on the NATIVE build platform so the multi-arch image
# never runs `apk add` under QEMU emulation (the slow part of an arm64 build). Both are
# architecture-independent data — the cert bundle Go reads at
# /etc/ssl/certs/ca-certificates.crt and the zoneinfo database — so they copy across.
FROM --platform=$BUILDPLATFORM alpine:3.21 AS certs
RUN apk add --no-cache ca-certificates tzdata

FROM alpine:3.21
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=certs /usr/share/zoneinfo /usr/share/zoneinfo
# addgroup/adduser/mkdir are busybox builtins in the base image (no package install), so
# this stays cheap even under emulation; wget (busybox) remains for the healthcheck.
RUN addgroup -S harbrr && adduser -S -G harbrr -H -u 1000 harbrr \
 && mkdir -p /config && chown harbrr:harbrr /config && chmod 700 /config

# --chmod guarantees the exec bit: in CI the `build` stage is replaced by a downloaded
# artifact (build-contexts), and GitHub artifacts don't preserve the executable mode.
COPY --from=build --chmod=0755 /out/harbrr /usr/local/bin/harbrr

USER harbrr
VOLUME ["/config"]
EXPOSE 7474

# Probes the management API liveness endpoint. If you set server.base_url, adjust
# the path accordingly (e.g. /harbrr/healthz).
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:7474/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["harbrr", "serve"]
CMD ["--host", "0.0.0.0", "--data-dir", "/config"]
