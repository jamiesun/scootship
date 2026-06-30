# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
ARG ALPINE_VERSION=3.22

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder

RUN apk add --no-cache ca-certificates

WORKDIR /src
COPY go.mod ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=""
RUN set -eu; \
    case "${TARGETARCH}${TARGETVARIANT:-}" in \
      amd64) goarm="" ;; \
      arm64) goarm="" ;; \
      armv7) goarm="7" ;; \
      *) echo "unsupported Docker target platform: ${TARGETARCH}${TARGETVARIANT:-}" >&2; exit 1 ;; \
    esac; \
    version="${VERSION:-0.1.0-dev}"; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" GOARM="${goarm}" \
      go build -trimpath \
        -ldflags "-s -w -X github.com/jamiesun/scootship/internal/version.Version=${version}" \
        -o /out/scootship \
        ./cmd/scootship
RUN mkdir -p /out/data

FROM busybox:1.37.0-musl AS runtime
WORKDIR /app

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/scootship /usr/local/bin/scootship
COPY --from=builder --chown=65532:65532 /out/data /data

ENV SCOOTSHIP_ADDR=:8080
ENV SCOOTSHIP_DATA_DIR=/data

USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["scootship"]
CMD ["serve"]

FROM alpine:${ALPINE_VERSION} AS runtime-alpine
WORKDIR /app

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/scootship /usr/local/bin/scootship
COPY --from=builder --chown=65532:65532 /out/data /data

ENV SCOOTSHIP_ADDR=:8080
ENV SCOOTSHIP_DATA_DIR=/data

USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["scootship"]
CMD ["serve"]

FROM runtime AS final
