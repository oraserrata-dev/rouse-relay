# syntax=docker/dockerfile:1

# --- Build stage ---------------------------------------------------------
# Pin the Go version to the latest stable Alpine-based image. CGO_ENABLED=0
# produces a fully static binary that runs on the minimal runtime stage
# below without any glibc shim.
FROM golang:1.23-alpine AS builder

# Build-time argument for the version stamped into the binary via ldflags.
# Pass with `docker build --build-arg VERSION=1.2.0` so `/health` reports
# the right version. Defaults to "dev" to match the Go fallback for
# unstamped builds, so a bare `docker build .` still works.
ARG VERSION=dev

WORKDIR /build
# Copy every Go source, not just main.go: the broadcast socket option lives
# in build-tagged socket_unix.go / socket_windows.go. The Linux build picks
# socket_unix.go via its !windows tag; socket_windows.go is copied but
# excluded by its build tag.
COPY go.mod *.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o rouse-relay .

# --- Runtime stage -------------------------------------------------------
FROM alpine:3.21

# CA certificates for any future outbound TLS calls. wget comes from
# busybox so HEALTHCHECK doesn't need a separate package.
RUN apk add --no-cache ca-certificates

COPY --from=builder /build/rouse-relay /usr/local/bin/rouse-relay

# Drop root inside the container. The relay only listens on UDP and
# unprivileged TCP (default port 9876), so no privileged operations are
# required.
USER nobody

EXPOSE 9876

# Container-level health probe. Hits the unauthenticated /health
# endpoint every 30s; Docker reports the container as unhealthy after
# three consecutive failures. Honors the PORT env var (falling back to
# 9876) so a remapped listen port doesn't make the container read as
# unhealthy forever.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O- "http://127.0.0.1:${PORT:-9876}/health" >/dev/null || exit 1

ENTRYPOINT ["rouse-relay"]
