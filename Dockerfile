# No "# syntax=" line on purpose: it would pull whatever Dockerfile frontend is
# current at build time. Everything used here is built into BuildKit and podman.

# The base image is pinned twice. The tag says which Go and Alpine release it
# is, the digest makes sure that this cannot change underneath. Dependabot
# proposes updates for both; the CI workflow reads the Go version from here.
#
# The build stage runs on the build machine's architecture and cross-compiles,
# so building for arm64 on amd64 needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine3.24@sha256:85dc1069ac644ea3c527b177303a406eb3358192816cd7f9e5848eb658851673 AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
# timetzdata       embeds the time zone database, which scratch does not have
# nethttpomithttp2 leaves out the HTTP/2 client; both APIs speak HTTP/1.1
ARG GO_TAGS=timetzdata,nethttpomithttp2

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
# The bundle is the one that ships with the pinned base image.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/basealert /basealert
# An empty directory owned by the runtime user. A named volume mounted here
# inherits the ownership, so the state file is writable without running as root.
COPY --from=build --chown=65534:65534 /out/data /data

# There is no /etc/passwd, hence the numeric user.
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/basealert"]
