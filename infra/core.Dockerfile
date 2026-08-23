FROM golang:1.24.5-bookworm AS build

WORKDIR /src/services/core
COPY services/core/go.mod services/core/go.sum ./
RUN go mod download
COPY services/core/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/omni-core .

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && useradd --system --uid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin omni \
    && mkdir -p /data \
    && chown omni:omni /data \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/omni-core /usr/local/bin/omni-core

WORKDIR /data
USER omni:omni
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/omni-core"]
CMD ["serve", "-db", "/data/omni-folio.db", "-addr", "0.0.0.0:8080"]
