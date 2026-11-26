# syntax=docker/dockerfile:1

# ---- build the builder ----------------------------------------------------
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY . .
# -short skips the test that compiles three full binaries (slow, memory-hungry).
RUN go test -short ./... \
 && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/openharness ./cmd/openharness

# ---- runtime image ----------------------------------------------------------
# The Go toolchain stays in the final image on purpose: every download is a
# fresh cross-compile of the agent runtime with the user's spec embedded.
FROM golang:1.26-bookworm

ENV GOTOOLCHAIN=local \
    GOPROXY=off \
    XDG_CACHE_HOME=/data/cache \
    GOCACHE=/data/cache/go-build \
    OPENHARNESS_ADDR=0.0.0.0:7070 \
    OPENHARNESS_PUBLIC=1 \
    OPENHARNESS_TRUST_PROXY=1

RUN useradd --create-home --uid 10001 app \
 && mkdir -p /data/cache \
 && chown -R app /data

COPY --from=build /out/openharness /usr/local/bin/openharness
USER app

# Fill the build cache for all six targets so the first downloads after a
# deploy take ~1s instead of ~6s. A fresh named volume mounted at /data is
# seeded with this content by Docker.
RUN openharness --warm

EXPOSE 7070
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:7070/healthz || exit 1

CMD ["openharness"]
