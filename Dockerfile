# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
RUN set -eu; \
    mkdir -p /out; \
    for cmd in . ./cmd/healthcheck; do \
        name=$(basename "$cmd"); [ "$name" = "." ] && name=api; \
        CGO_ENABLED=0 GOOS=linux go build \
            -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/

ENV PORT=4102 \
    AUTO_MIGRATE=true \
    LOG_FORMAT=json \
    AUTH_MODE=gateway \
    GIN_MODE=release

EXPOSE 4102
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
  CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/api"]
