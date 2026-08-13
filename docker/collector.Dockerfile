# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG INVS_GIT_COMMIT=unknown
RUN printf '%s\n' "$INVS_GIT_COMMIT" | grep -Eq '^(unknown|[0-9a-f]{40})$'
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collector ./cmd/collector && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/reconcile ./cmd/reconcile

FROM alpine:3.22
ARG INVS_GIT_COMMIT=unknown
ENV INVS_GIT_COMMIT=$INVS_GIT_COMMIT
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S collector && adduser -S -G collector collector
COPY --from=build /out/collector /usr/local/bin/collector
COPY --from=build /out/reconcile /usr/local/bin/reconcile
COPY --chmod=0444 config/config.example.yaml /etc/invs/config.yaml
COPY docker/collector-entrypoint.sh /usr/local/bin/collector-entrypoint
RUN chmod 0755 /etc/invs /usr/local/bin/collector-entrypoint && \
    chmod 0444 /etc/invs/config.yaml && \
    mkdir -p /data && chown collector:collector /data
USER collector
WORKDIR /data
ENTRYPOINT ["collector-entrypoint"]
