# syntax=docker/dockerfile:1.7
# Build stage: static binary, reproducible, cached module downloads.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/analytics ./cmd/analytics

# Runtime stage: distroless, non-root, no shell.
FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/DemonIngvi/proview-analytics" \
      org.opencontainers.image.description="ProView analytics: admin dashboard and slice API over ClickHouse aggregates" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/analytics /usr/local/bin/analytics
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/analytics"]
