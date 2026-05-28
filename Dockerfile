# syntax=docker/dockerfile:1.7
#
# Targets:
#   standalone (default) — iag-project-management repo root on Railway
#   monorepo             — IAG_multi_backend root context (deploy/docker-compose)
#
# Monorepo:   docker build -f services/commercial/project-management/Dockerfile --target monorepo .
# Standalone: docker build --target standalone .

FROM golang:1.25-alpine AS base
RUN apk add --no-cache git ca-certificates
ENV PLATFORM_GO_DEP=/deps/platform-go

FROM base AS platform-go-clone
ARG IAG_META_REF=main
ARG IAG_META_REPO=https://github.com/AlexanderKiyingi/IAG_multi_backend.git
RUN git clone --depth 1 --branch "${IAG_META_REF}" "${IAG_META_REPO}" /tmp/iag \
    && mv /tmp/iag/shared/platform-go "${PLATFORM_GO_DEP}" \
    && rm -rf /tmp/iag

FROM base AS platform-go-copy
COPY shared/platform-go ${PLATFORM_GO_DEP}

FROM base AS build-standalone
COPY --from=platform-go-clone ${PLATFORM_GO_DEP} ${PLATFORM_GO_DEP}
WORKDIR /src
COPY go.mod go.sum ./
COPY pkg/authclient ./pkg/authclient
RUN go mod edit -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP} \
    && go mod download
COPY . .
ARG VERSION=dev
RUN set -eu; \
    mkdir -p /out; \
    for cmd in . ./cmd/healthcheck ./cmd/jobs; do \
        name=$(basename "$cmd"); [ "$name" = "." ] && name=api; \
        CGO_ENABLED=0 GOOS=linux go build \
            -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

FROM base AS build-monorepo
COPY --from=platform-go-copy ${PLATFORM_GO_DEP} ${PLATFORM_GO_DEP}
WORKDIR /src/services/commercial/project-management
COPY services/commercial/project-management/go.mod services/commercial/project-management/go.sum ./
COPY services/commercial/project-management/pkg/authclient ./pkg/authclient
RUN go mod edit -replace=github.com/alvor-technologies/iag-platform-go=${PLATFORM_GO_DEP} \
    && go mod download
COPY services/commercial/project-management/ .
ARG VERSION=dev
RUN set -eu; \
    mkdir -p /out; \
    for cmd in . ./cmd/healthcheck ./cmd/jobs; do \
        name=$(basename "$cmd"); [ "$name" = "." ] && name=api; \
        CGO_ENABLED=0 GOOS=linux go build \
            -trimpath \
            -ldflags="-s -w -X main.version=${VERSION}" \
            -o "/out/$name" "$cmd"; \
    done

FROM gcr.io/distroless/static-debian12:nonroot AS monorepo
WORKDIR /app
COPY --from=build-monorepo /out/ /app/
ENV PORT=4102 \
    AUTO_MIGRATE=true \
    LOG_FORMAT=json \
    GIN_MODE=release
EXPOSE 4102
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
  CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/api"]

FROM gcr.io/distroless/static-debian12:nonroot AS standalone
WORKDIR /app
COPY --from=build-standalone /out/ /app/
ENV PORT=4102 \
    AUTO_MIGRATE=true \
    LOG_FORMAT=json \
    GIN_MODE=release
EXPOSE 4102
HEALTHCHECK --interval=15s --timeout=5s --start-period=30s --retries=5 \
  CMD ["/app/healthcheck"]
ENTRYPOINT ["/app/api"]
