# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.3 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY *.go index.html ./
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
# The cache mounts keep compiled dependencies (mostly tailscale.com) between builds.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/tailhome . && \
    mkdir /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/tailhome /usr/local/bin/tailhome
# Owned by nonroot so named volumes work without a chown.
COPY --from=builder --chown=65532:65532 /out/data /data
ENV TAILHOME_STATE_DIR=/data TAILHOME_OVERRIDES=/data/overrides.yaml
VOLUME /data
USER nonroot
ENTRYPOINT ["/usr/local/bin/tailhome"]
