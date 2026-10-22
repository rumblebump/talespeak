# syntax=docker/dockerfile:1
# Cross-compiles on the build machine's own architecture (no QEMU) and ships
# one static binary on a distroless base. The web client and the demo pack
# are embedded in the binary with go:embed; the other story packs from
# content/ are copied to /packs, where the lobby offers them.

# renovate: datasource=docker depName=golang versioning=docker
ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
# go.sum appears with the first dependency
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/talespeak ./cmd/talespeak
# Data directory owned by the distroless nonroot user, so the volume starts writable
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/talespeak /talespeak
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /src/content/clockmaker /packs/clockmaker
ENV TALESPEAK_DATA=/data TALESPEAK_LISTEN=:8080 TALESPEAK_PACKS=/packs
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=3s CMD ["/talespeak", "healthcheck"]
ENTRYPOINT ["/talespeak"]
CMD ["serve"]
