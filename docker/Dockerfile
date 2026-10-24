# Multi-stage Docker build for HttpSMS Gateway Server

# Stage 1: Builder
FROM golang:alpine AS builder

WORKDIR /src

# Install build tools and certificates
RUN apk add --no-cache git ca-certificates tzdata

COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/httpsms-server ./cmd/server

# Stage 2: Runtime
FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S httpsms && adduser -S httpsms -G httpsms \
    && mkdir -p /app/data /app/data/ota /app/web \
    && chown -R httpsms:httpsms /app

COPY --from=builder /bin/httpsms-server /app/httpsms-server
COPY web/ /app/web/

ENV PORT=8080 \
    DB_PATH=/app/data/httpsms.db \
    OTA_DIR=/app/data/ota \
    WEB_DIR=/app/web \
    MASTER_SECRET=htsms_admin_secret_change_me

EXPOSE 8080

VOLUME ["/app/data"]

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/v1/health || exit 1

ENTRYPOINT ["/app/httpsms-server"]

