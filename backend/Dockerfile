# syntax=docker/dockerfile:1.7

# ---- build ------------------------------------------------------------------
FROM golang:1.24-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git

# Resolve dependencies first so this layer is cached until go.mod changes.
# `go mod tidy` also writes go.sum, so the repository does not need to ship one.
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod tidy && go mod download

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---- runtime ------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
