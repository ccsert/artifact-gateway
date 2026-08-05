FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=artifact-gateway-go-mod,target=/go/pkg/mod,sharing=locked \
    go mod download
COPY . .
RUN --mount=type=cache,id=artifact-gateway-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=artifact-gateway-go-build,target=/root/.cache/go-build,sharing=locked \
    go test ./...
RUN --mount=type=cache,id=artifact-gateway-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=artifact-gateway-go-build,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /gateway ./cmd/gateway
RUN --mount=type=cache,id=artifact-gateway-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=artifact-gateway-go-build,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /gateway-healthcheck ./cmd/healthcheck

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /gateway /gateway
COPY --from=build /gateway-healthcheck /gateway-healthcheck
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/gateway"]
