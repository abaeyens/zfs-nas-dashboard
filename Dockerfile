# Production image — two stages.
#
# Build stage compiles a fully static binary (CGO_ENABLED=0; all deps are
# pure Go). Runtime stage carries only the binary plus the system tools the
# dashboard shells out to. Final image is roughly 150 MB versus 800 MB for
# the dev image (which has the Go toolchain).

FROM golang:1.26-bookworm AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
    -buildvcs=false \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/zfs-nas-dashboard \
    ./cmd/zfs-nas-dashboard


FROM debian:bookworm-slim

RUN echo "deb http://deb.debian.org/debian bookworm contrib" >> /etc/apt/sources.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends \
        smartmontools \
        zfsutils-linux \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/zfs-nas-dashboard /usr/local/bin/zfs-nas-dashboard

CMD ["/usr/local/bin/zfs-nas-dashboard"]
