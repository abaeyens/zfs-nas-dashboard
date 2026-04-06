FROM golang:1.26.1-bookworm

RUN echo "deb http://deb.debian.org/debian bookworm contrib" >> /etc/apt/sources.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends smartmontools zfsutils-linux \
    && rm -rf /var/lib/apt/lists/*

# Cache module downloads as a separate layer so rebuilds after code-only
# changes don't re-download dependencies.
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

# Build the binary into /usr/local/bin so it is NOT shadowed by the
# source bind-mount at /app used in development.
COPY . .
RUN go build -buildvcs=false -o /usr/local/bin/zfs-nas-dashboard ./cmd/zfs-nas-dashboard

# /app is the working directory; in production nothing is mounted here.
# In development the source tree is bind-mounted at /app for live editing.
WORKDIR /app
CMD ["/usr/local/bin/zfs-nas-dashboard"]
