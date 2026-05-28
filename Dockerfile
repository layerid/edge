# syntax=docker/dockerfile:1.7
# layerid-edge — Go scoring runtime.
#
# Multi-stage: build the static binary in a Go 1.25 image, drop it into
# distroless for a tiny runtime. The binary is the only runtime
# artifact — no Postgres driver, no JWKS at this phase, just the HTTP
# server.

FROM golang:1.25-alpine AS build

WORKDIR /src

# Cache modules separately from sources.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# CGO off so the binary is fully static and runs on distroless:nonroot
# without glibc shenanigans.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/layerid-edge \
    ./cmd/layerid-edge

# ── Runtime ──────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/layerid-edge /app/layerid-edge

EXPOSE 8080

ENTRYPOINT ["/app/layerid-edge"]
