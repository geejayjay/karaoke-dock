# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
ARG VERSION=dev
ENV VERSION=${VERSION}
RUN npm install -g pnpm@10
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN mkdir -p /src/server/internal/webui/dist && pnpm build

FROM --platform=$BUILDPLATFORM golang:bookworm AS server
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
ENV GOTOOLCHAIN=auto CGO_ENABLED=0
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/server/internal/webui/dist ./internal/webui/dist
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/karaokedock ./cmd/karaokedock

FROM debian:bookworm-slim
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl ffmpeg git libgomp1 libsndfile1 unzip \
  && rm -rf /var/lib/apt/lists/*

COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/

# Install yt-dlp and Deno matching x86_64 or arm64 (Raspberry Pi)
RUN set -eux; \
  case "${TARGETARCH}" in \
    arm64) ytdlp_bin="yt-dlp_linux_aarch64"; deno_arch="aarch64-unknown-linux-gnu" ;; \
    *)     ytdlp_bin="yt-dlp_linux";         deno_arch="x86_64-unknown-linux-gnu" ;; \
  esac; \
  curl -fsSL "https://github.com/yt-dlp/yt-dlp/releases/latest/download/${ytdlp_bin}" -o /usr/local/bin/yt-dlp; \
  chmod +x /usr/local/bin/yt-dlp; \
  curl -fsSL "https://github.com/denoland/deno/releases/latest/download/deno-${deno_arch}.zip" -o /tmp/deno.zip; \
  unzip -q /tmp/deno.zip -d /usr/local/bin; \
  chmod +x /usr/local/bin/deno; \
  rm -f /tmp/deno.zip

WORKDIR /opt/karaokedock/sidecar
COPY sidecar/pyproject.toml sidecar/uv.lock ./
COPY sidecar/karaokedock_ml ./karaokedock_ml
RUN apt-get update && apt-get install -y --no-install-recommends build-essential \
  && UV_PYTHON_INSTALL_DIR=/opt/karaokedock/sidecar/.python \
     uv sync --extra cpu --python 3.12 --managed-python --no-dev \
  && apt-get purge -y --auto-remove build-essential \
  && rm -rf /var/lib/apt/lists/* /root/.cache/uv

# The AI models are part of the image, so a new container starts ready with
# nothing to download. The entrypoint copies them into the data volume.
RUN uv run --no-sync python -m karaokedock_ml.fetch_models /opt/karaokedock/models \
  && rm -rf /root/.cache

COPY --from=server /out/karaokedock /usr/local/bin/karaokedock
COPY deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN sed -i 's/\r$//' /usr/local/bin/docker-entrypoint.sh && chmod +x /usr/local/bin/docker-entrypoint.sh

ENV KARAOKEDOCK_PORT=7464 \
    KARAOKEDOCK_METADATA=/data/metadata \
    KARAOKEDOCK_SIDECAR=/opt/karaokedock/sidecar

VOLUME ["/data"]
EXPOSE 7464/tcp 7464/udp
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
