# syntax=docker/dockerfile:1

# --- build stage ---
# Build on the host platform and cross-compile for the target so multi-arch
# images need no QEMU emulation (Go does the cross-compiling natively).
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src

# Cache module metadata (no third-party deps, but keeps layers stable).
COPY go.mod ./
RUN go mod download

COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags "-s -w -X main.version=${VERSION}" -o /tokps .

# --- runtime stage ---
# distroless/static ships CA certificates, so outbound HTTPS works.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tokps /usr/local/bin/tokps
ENTRYPOINT ["tokps"]
