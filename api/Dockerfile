# syntax=docker/dockerfile:1

# --- build -------------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Copied separately so the module cache layer survives source edits. With zero
# dependencies today this is nearly free; it stops being free in phase 2.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 gives a static binary, which is what makes the scratch stage
# below viable. -trimpath keeps build-host paths out of panic traces.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/api ./cmd/api

# --- run ---------------------------------------------------------------------
FROM scratch

# Needed to dial Postgres and Google's APIs over TLS; scratch has no trust store.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# No zoneinfo is copied in: cmd/api imports time/tzdata, so the binary carries
# the database itself. That keeps the timezone model working regardless of what
# the base image provides, and startup fails loudly via tz.Available if it ever
# stops being true.
COPY --from=build /out/api /api

ENV PORT=8080
EXPOSE 8080

USER 65534:65534

ENTRYPOINT ["/api"]
