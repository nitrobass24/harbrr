# syntax=docker/dockerfile:1
#
# Multi-stage build for harbrr. The binary is pure-Go (CGO_ENABLED=0), so the
# final image is a tiny non-root Alpine with just the static binary.

# The build stage stays on the native runner platform (BUILDPLATFORM) and Go
# cross-compiles to the requested target (TARGETOS/TARGETARCH, injected by buildx),
# so a multi-arch build never pays for an emulated Go toolchain. CGO is off, so this
# is a pure cross-compile. For a plain `docker build` the target args are empty and
# Go builds for the host.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src

# Cache module downloads separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The Go build embeds web/dist (the compiled SPA). A plain local `docker build` from a
# fresh clone has an empty web/dist (dist/.gitkeep only), which would otherwise silently
# ship a UI-less binary whose pages answer "frontend not built". Fail loudly instead:
# build the SPA first (`make web-build`) so `docker build` picks it up from the context,
# or pull a published image. In CI this whole `build` stage is replaced by a
# prebuilt-binary build-context, so this guard never runs there.
RUN test -f web/dist/index.html || { \
      echo "ERROR: web/dist is empty — the management UI is not built."; \
      echo "Run 'make web-build' (needs bun) before 'docker build', or pull a published ghcr.io/autobrr/harbrr image."; \
      exit 1; \
    }

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

# Everything the final image needs that would otherwise require running a shell in the
# target architecture, assembled here on the NATIVE build platform. Same alpine version
# as the final stage, so the artifacts copy across unchanged:
#   - ca-certificates + tzdata: architecture-independent data (the cert bundle Go reads
#     at /etc/ssl/certs/ca-certificates.crt, and the zoneinfo database)
#   - the harbrr account and its /config dir: plain text in /etc/passwd + /etc/group and
#     a directory with a mode, none of which care about architecture
# Keeping this stage native is what lets the final stage hold zero RUN instructions, so a
# multi-arch build needs no QEMU at all — see the note above the final stage.
FROM --platform=$BUILDPLATFORM alpine:3.24 AS rootfs
RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S harbrr && adduser -S -G harbrr -H -u 1000 harbrr \
 && mkdir -p /config && chown harbrr:harbrr /config && chmod 700 /config

# This stage has NO RUN instructions, only COPY — deliberately. A RUN here would need a
# shell in the target architecture, which on an amd64 builder means emulating arm64, and
# `docker/setup-qemu-action` cost ~17s per arm64 CI run to install those binfmt handlers.
# Everything is prepared natively in `rootfs` above and copied in. wget (busybox) is in
# the base image already for the healthcheck. Keep it RUN-free.
FROM alpine:3.24
COPY --from=rootfs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=rootfs /usr/share/zoneinfo /usr/share/zoneinfo
# passwd/group carry the harbrr account. Both stages are the same alpine version, so these
# are the base image's own files plus that one account.
COPY --from=rootfs /etc/passwd /etc/passwd
COPY --from=rootfs /etc/group /etc/group
# Numeric owner: the USER name below resolves via the passwd copied above, but --chown
# resolution during COPY should not depend on the order of preceding layers.
COPY --from=rootfs --chown=1000:1000 --chmod=700 /config /config

# --chmod guarantees the exec bit: in CI the `build` stage is replaced by a downloaded
# artifact (build-contexts), and GitHub artifacts don't preserve the executable mode.
COPY --from=build --chmod=0755 /out/harbrr /usr/local/bin/harbrr

USER harbrr
VOLUME ["/config"]
EXPOSE 7478

# Probes the management API liveness endpoint. If you set server.base_url, adjust
# the path accordingly (e.g. /harbrr/healthz).
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:7478/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["harbrr", "serve"]
CMD ["--host", "0.0.0.0", "--data-dir", "/config"]
