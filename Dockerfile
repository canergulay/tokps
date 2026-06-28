# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.23-alpine AS build
WORKDIR /src

# Cache module metadata (no third-party deps, but keeps layers stable).
COPY go.mod ./
RUN go mod download

COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /tokps .

# --- runtime stage ---
# distroless/static ships CA certificates, so outbound HTTPS works.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tokps /usr/local/bin/tokps
ENTRYPOINT ["tokps"]
