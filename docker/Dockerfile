FROM node:20-alpine AS ui-builder

WORKDIR /ui

# Copy UI package files
COPY ui/package.json ui/package-lock.json ./

# Install dependencies (including devDependencies needed for build)
RUN npm ci --include=dev

# Copy UI source files
COPY ui/ ./

# Build the UI
RUN npm run build

#build rust booster
# FROM rust:1.92-bookworm AS booster-builder

# WORKDIR /src/booster

# RUN rustup target add wasm32-wasip1

# COPY booster/Cargo.toml booster/Cargo.lock ./
# COPY booster/src ./src
# COPY booster/components ./components

# RUN --mount=type=cache,target=/usr/local/cargo/registry \
#     --mount=type=cache,target=/usr/local/cargo/git \
#     cargo build --release --manifest-path components/Cargo.toml --target wasm32-wasip1

# RUN --mount=type=cache,target=/usr/local/cargo/registry \
#     --mount=type=cache,target=/usr/local/cargo/git \
#     cargo build --release


FROM golang:1.24-bookworm AS builder

WORKDIR /src

# Install build dependencies including C++ standard library for WasmEdge
# Bookworm has LLVM 14 in main repos, no external repo needed
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    git \
    g++ \
    ca-certificates \
    llvm-14-dev \
    liblld-14-dev \
    && rm -rf /var/lib/apt/lists/*

# Add build argument to control CGO (set to 1 to enable WASM support)
ARG CGO_ENABLED=1

# Copy dependency files first for better layer caching
COPY go.mod go.sum ./
COPY dbx/go.mod dbx/go.sum ./dbx/
COPY tygoja/go.mod tygoja/go.sum ./tygoja/
COPY freecache/go.mod freecache/go.sum ./freecache/
COPY chromem-go/go.mod chromem-go/go.sum ./chromem-go/
COPY langchaingo/go.mod langchaingo/go.sum ./langchaingo/
COPY graphql-go/go.mod graphql-go/go.sum ./graphql-go/
COPY ws/go.mod ws/go.sum ./ws/
COPY rueidis/go.mod rueidis/go.sum ./rueidis/

# Optimize Go module downloads with multiple strategies:
# 1. BuildKit cache mount - persists module cache between builds
# 2. GOPROXY=direct - bypasses proxy for faster direct downloads (or use a proxy if you have one)
# 3. GOSUMDB=sum.golang.org - uses checksum database (or 'off' if security allows)
# 4. GOMODCACHE - explicit cache location
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOPROXY=direct \
    GOSUMDB=sum.golang.org \
    GOMODCACHE=/go/pkg/mod \
    go mod download

# Copy remaining source code (excluding files listed in .dockerignore)
# Note: ui/dist/ is excluded in .dockerignore, so our built dist won't be overwritten
COPY . .

# Copy the built UI from ui-builder stage (overwrites any existing dist)
COPY --from=ui-builder /ui/dist ./ui/dist

# If CGO is enabled and wasmedge directory exists, copy WasmEdge libraries and headers for build-time linking
RUN if [ "$CGO_ENABLED" = "1" ] && [ -d "wasmedge" ]; then \
        mkdir -p /usr/local/lib /usr/local/include && \
        cp -r wasmedge/lib/* /usr/local/lib/ 2>/dev/null || true && \
        cp -r wasmedge/include/* /usr/local/include/ 2>/dev/null || true && \
        echo "WasmEdge libraries and headers copied"; \
    else \
        echo "WasmEdge directory not found or CGO disabled, building without WasmEdge support"; \
    fi

ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux \
    GOARCH=amd64 \
    GOFLAGS="-mod=mod"

# CRITICAL: Build and clean source in the same RUN command
# This prevents source code from being saved in a separate layer
# Use both module cache and build cache mounts for faster builds
# Set CGO flags if CGO is enabled and WasmEdge is available
# Note: WasmEdge is a C++ library, so we need to link libstdc++
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    ( \
        if [ "$CGO_ENABLED" = "1" ] && [ -d "/usr/local/include/wasmedge" ]; then \
            export CGO_CFLAGS="-I/usr/local/include" && \
            export CGO_LDFLAGS="-L/usr/local/lib -lwasmedge -lstdc++ -lm -ldl -lpthread" && \
            export LD_LIBRARY_PATH="/usr/local/lib:${LD_LIBRARY_PATH}" && \
            export CXX=g++ && \
            echo "Building with CGO and WasmEdge support"; \
        else \
            echo "Building without CGO or WasmEdge"; \
        fi && \
        go build -o /out/bosbase ./examples/base \
    ) && \
    find /src -mindepth 1 -delete && \
    test -f /out/bosbase

# FROM alpine:latest

# RUN apk add --no-cache ca-certificates tzdata wget

FROM python:3.12-slim-bookworm


# Install runtime dependencies for CGO binaries
# libstdc++ is needed for WasmEdge C++ library
# libllvm14 is needed at runtime because libwasmedge.so links against it
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libstdc++6 \
    libllvm14 \
    curl \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

# Install uv.
COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /bin/

# Copy only the compiled binary from builder stage (no source code)
COPY --from=builder /out/bosbase /pb/bosbase

#rust booster
# COPY --from=booster-builder /src/booster/target/release/booster /pb/booster
# COPY --from=booster-builder /src/booster/components/target/wasm32-wasip1/release/*.wasm /pb/booster-wasm/


# Copy dependency files first for better layer caching
COPY functions/pyproject.toml functions/uv.lock /pb/functions/

# Set uv environment variables for caching
ENV UV_LINK_MODE=copy \
    UV_PYTHON_CACHE_DIR=/root/.cache/uv/python

# Install dependencies (without the project itself) for better layer caching
WORKDIR /pb/functions
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --locked --no-install-project

# Copy the rest of the functions directory
COPY functions/ /pb/functions

# Sync the project itself
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --locked


COPY wasmedge /usr/local
COPY docker/supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY docker/start-gunicorn.sh /pb/start-gunicorn.sh
COPY docker/start-bosbase.sh /pb/start-bosbase.sh
# COPY docker/start-booster.sh 
RUN chmod +x /pb/start-gunicorn.sh /pb/start-bosbase.sh 
#/pb/start-booster.sh
# RUN chmod +x /pb/start-booster.sh

ENV PATH="/usr/local/bin:${PATH}"
ENV LD_LIBRARY_PATH="/usr/local/lib:${LD_LIBRARY_PATH}"
# Uncomment to bake in project migrations or hooks.
# COPY ./pb_migrations /pb/pb_migrations
# COPY ./pb_hooks /pb/pb_hooks

VOLUME /pb/pb_data
EXPOSE 8090 8000 2678

CMD ["/usr/bin/supervisord", "-c", "/etc/supervisor/conf.d/supervisord.conf"]
