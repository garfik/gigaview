FROM golang:bookworm AS builder

ARG VIPS_VERSION=8.17.2
ENV CGO_ENABLED=1
    
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    pkg-config \
    meson \
    ninja-build \
    wget \
    ca-certificates \
    libglib2.0-dev \
    libexpat1-dev \
    libjpeg-dev \
    libpng-dev \
    libtiff-dev \
    libwebp-dev \
    liborc-dev \
    liblcms2-dev \
    libexif-dev \
    && rm -rf /var/lib/apt/lists/*
    
WORKDIR /tmp
RUN wget -q https://github.com/libvips/libvips/releases/download/v${VIPS_VERSION}/vips-${VIPS_VERSION}.tar.xz \
    && tar -xJf vips-${VIPS_VERSION}.tar.xz \
    && rm vips-${VIPS_VERSION}.tar.xz
    
WORKDIR /tmp/vips-${VIPS_VERSION}
RUN meson setup build \
      --prefix=/usr/local \
      --libdir=lib \
      -Dintrospection=disabled \
      -Ddeprecated=false \
      -Dmodules=disabled \
      -Dmagick=disabled \
      -Dpdfium=disabled \
      -Dpoppler=disabled \
      -Dopenslide=disabled \
      -Dopenexr=disabled \
      -Dcfitsio=disabled \
      -Dfftw=disabled \
      -Dmatio=disabled

RUN ninja -C build && ninja -C build install && ldconfig
    
ENV PKG_CONFIG_PATH=/usr/local/lib/pkgconfig
RUN pkg-config --modversion vips
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux go build -o gigaview ./cmd/server

# Runtime stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    libglib2.0-dev \
    libexpat1-dev \
    libjpeg-dev \
    libpng-dev \
    libtiff-dev \
    libwebp-dev \
    liborc-dev \
    liblcms2-dev \
    libexif-dev \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Copy libvips from builder
COPY --from=builder /usr/local/lib /usr/local/lib
COPY --from=builder /usr/local/bin/vips /usr/local/bin/vips
RUN ldconfig

WORKDIR /app

COPY --from=builder /build/gigaview .
COPY --from=builder /build/public ./public

VOLUME /data

# Number of OS threads Go scheduler may run
# Can be overridden via environment variable (defaults to number of CPU cores)
# ENV GOMAXPROCS=2
# Controls parallel image processing inside libvips
ENV VIPS_CONCURRENCY=1
# Sets libvips internal cache size (MB)
ENV VIPS_MAX_CACHE_MB=256
# How many tiles are rendered in parallel during warmup
ENV WARMUP_WORKERS=2
# Go memory tuning (can be overridden via environment variables):
# GOMEMLIMIT caps Go heap usage around the target (soft limit, e.g., 400MiB, 1GiB)
# GOGC controls GC aggressiveness (default 100, lower = more frequent GC)
# ENV GOMEMLIMIT=400MiB
# ENV GOGC=50

EXPOSE 8080

CMD ["./gigaview"]

