# syntax=docker/dockerfile:1

# ---------- build ----------
FROM golang:1.27.3-alpine3.24 AS builder

WORKDIR /src

# Dependencies first, so this layer caches independently of source changes.
# go mod verify checks the downloaded modules against go.sum.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

COPY . .

# TARGETOS/TARGETARCH are supplied by BuildKit, so the same Dockerfile builds
# for amd64 and arm64. CGO off produces a fully static binary, which is what
# lets the runtime stage carry nothing but the binary and the CA bundle.
# -trimpath drops build-host paths, -s -w drop the symbol table and DWARF.
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -buildvcs=false -ldflags='-s -w' -o /out/ogc-api .

# ---------- runtime ----------
FROM alpine:3.24.3

# ca-certificates is required: plot-bag-matching-pdok calls the PDOK BAG WFS
# over HTTPS, and a bare Alpine image has no trust store, so every request
# fails with "x509: certificate signed by unknown authority".
RUN apk add --no-cache ca-certificates \
 && addgroup -g 10001 -S app \
 && adduser -u 10001 -S -G app -H -s /sbin/nologin app

# Owned by root and not writable by the app user, so the process cannot
# overwrite its own binary.
COPY --from=builder --chown=root:root --chmod=0555 /out/ogc-api /usr/local/bin/ogc-api

USER 10001:10001

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-8080}/" || exit 1

ENTRYPOINT ["/usr/local/bin/ogc-api"]
