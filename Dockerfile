# syntax=docker/dockerfile:1
#
# The hosted MCP server: docker build -t pantech-mcp --build-arg VERSION=v0.1.0 .
#
# It needs github.com/Pantech-Dynamics/pantech-go v0.1.0 to be published.
# Until then, build against a local checkout by overriding the empty
# "pantech-go" stage below with a named build context:
#
#   docker buildx build --build-context pantech-go=<path to pantech-go> -t pantech-mcp .
#
# The replace that makes is applied inside the build only, never in the repo.

FROM scratch AS pantech-go

# Built on the runner's own platform and cross-compiled (CGO is off), so the
# arm64 image does not compile under emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
# git lets go fetch a module straight from its repository when the module
# proxy does not have it yet (a release minutes old can briefly be missing).
RUN apk add --no-cache git
WORKDIR /src
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
COPY --from=pantech-go / /pantech-go/
COPY . .
RUN if [ -f /pantech-go/go.mod ]; then \
      go mod edit -replace github.com/Pantech-Dynamics/pantech-go=/pantech-go && go mod tidy; \
    fi && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath \
      -ldflags "-s -w -X github.com/Pantech-Dynamics/pantech-mcp/mcp.Version=${VERSION#v}" \
      -o /out/pantech-mcp ./cmd/pantech-mcp

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/pantech-mcp /pantech-mcp
USER nonroot:nonroot
EXPOSE 8080
ENV MCP_ADDR=:8080
ENTRYPOINT ["/pantech-mcp", "http"]
