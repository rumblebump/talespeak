# syntax=docker/dockerfile:1
# Cross-compiles on the build machine's own architecture (no QEMU) and ships
# one static binary on a distroless base. The web client is embedded in the
# binary with go:embed, so the image needs nothing else.

ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/talespeak ./cmd/talespeak

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/talespeak /talespeak
ENV TALESPEAK_DATA=/data TALESPEAK_LISTEN=:8080
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=3s CMD ["/talespeak", "healthcheck"]
ENTRYPOINT ["/talespeak"]
CMD ["serve"]
