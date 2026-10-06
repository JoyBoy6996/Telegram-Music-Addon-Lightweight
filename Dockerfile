# Build stage
FROM golang:alpine AS builder

WORKDIR /build

# Install CA certificates for MTProto TLS connections
RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build stripped static binary without cgo
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -extldflags '-static'" \
    -trimpath \
    -o telegram-music-addon .

# Final runtime stage: minimal non-root distroless image
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

# Copy CA certs and timezones
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy compiled binary and icon
COPY --from=builder /build/telegram-music-addon /app/telegram-music-addon
COPY --from=builder /build/icon.png /app/icon.png

# Target 128 MB RAM container with GC tuning
ENV GOMEMLIMIT=90MiB
ENV GOGC=50
ENV PORT=3000

EXPOSE 3000

USER nonroot:nonroot

ENTRYPOINT ["/app/telegram-music-addon"]

