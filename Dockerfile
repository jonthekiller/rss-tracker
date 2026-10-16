# syntax=docker/dockerfile:1

# ---- Build stage ----
# Cross-compiles on the build platform (no QEMU needed for arm64: the Go
# toolchain targets GOARCH directly; CGO is disabled so the binary is static).
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
  go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
  -o /out/bot ./cmd/bot

# ---- Runtime stage ----
# Distroless static: no shell, no package manager, ~2 MB base image. It ships
# CA certificates, tzdata and a nonroot user (uid 65532) — the bot binary is
# fully static and handles SIGTERM itself (no dumb-init needed).
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app

COPY --from=build /out/bot /app/bot

# Readable by any runtime UID (the container may run as the host user).
# (COPY preserves the 0755 mode; /app/data is created by the bot on start.)
VOLUME ["/app/data"]

# start-period covers Discord login on restart: with an existing database,
# checks before the bot is ready would otherwise count as failures.
# The healthcheck is a subcommand of the same binary (no Node, no shell).
HEALTHCHECK --interval=60s --timeout=10s --start-period=120s --retries=3 \
  CMD ["/app/bot", "healthcheck"]

ENTRYPOINT ["/app/bot"]
CMD ["run"]
