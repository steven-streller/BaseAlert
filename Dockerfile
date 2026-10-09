# syntax=docker/dockerfile:1

# The build stage runs on the build machine's architecture and cross-compiles,
# so building for arm64 on amd64 needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
# timetzdata       embeds the time zone database, which scratch does not have
# nethttpomithttp2 leaves out the HTTP/2 client; both APIs speak HTTP/1.1
ARG GO_TAGS=timetzdata,nethttpomithttp2

RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags "$GO_TAGS" -ldflags "-s -w -X main.version=$VERSION" -o /out/basealert . \
 && mkdir /out/data

FROM scratch
LABEL org.opencontainers.image.title="BaseAlert" \
      org.opencontainers.image.description="Pushover notifications when your DJs go live on WeAreOne.FM"

# scratch has no CA certificates; without them every HTTPS request fails.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/basealert /basealert
# An empty directory owned by the runtime user. A named volume mounted here
# inherits the ownership, so the state file is writable without running as root.
COPY --from=build --chown=65534:65534 /out/data /data

# There is no /etc/passwd, hence the numeric user.
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/basealert"]
