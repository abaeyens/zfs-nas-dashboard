FROM golang:1.24-bookworm

RUN echo "deb http://deb.debian.org/debian bookworm contrib" >> /etc/apt/sources.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends smartmontools zfsutils-linux \
    && rm -rf /var/lib/apt/lists/*

# TODO: add non-root user (uid 1000) + sudoers rule for smartctl once ready to harden
# RUN useradd -m -u 1000 app
# USER app

WORKDIR /app

CMD ["go", "run", "./cmd/nas-dashboard"]
