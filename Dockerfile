FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
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
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X github.com/artifact-gateway/artifact-gateway/internal/buildinfo.injectedVersion=$VERSION -X github.com/artifact-gateway/artifact-gateway/internal/buildinfo.injectedRevision=$REVISION" \
    -o /gateway ./cmd/gateway
RUN --mount=type=cache,id=artifact-gateway-go-mod,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,id=artifact-gateway-go-build,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -buildvcs=false \
    -ldflags="-s -w -X github.com/artifact-gateway/artifact-gateway/internal/buildinfo.injectedVersion=$VERSION -X github.com/artifact-gateway/artifact-gateway/internal/buildinfo.injectedRevision=$REVISION" \
    -o /gateway-healthcheck ./cmd/healthcheck

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG VERSION=dev
ARG REVISION=unknown
ARG SOURCE=https://github.com/ccsert/artifact-gateway
LABEL org.opencontainers.image.title="Artifact Gateway" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION" \
      org.opencontainers.image.source="$SOURCE"
COPY --from=build /gateway /gateway
COPY --from=build /gateway-healthcheck /gateway-healthcheck
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/gateway"]
