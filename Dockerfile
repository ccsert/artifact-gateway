FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /gateway ./cmd/gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /gateway-healthcheck ./cmd/healthcheck

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /gateway /gateway
COPY --from=build /gateway-healthcheck /gateway-healthcheck
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/gateway"]
