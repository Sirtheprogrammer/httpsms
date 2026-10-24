# Multi-stage Docker build for HttpSMS Gateway Server + React dashboard

# Stage 1: dashboard
FROM node:20-alpine AS dashboard
WORKDIR /dashboard
COPY dashboard/package.json dashboard/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY dashboard/ ./
RUN npm run build

# Stage 2: Go server
FROM golang:alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates tzdata
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/httpsms-server ./cmd/server

# Stage 3: runtime
FROM alpine:3.20
WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S httpsms && adduser -S httpsms -G httpsms \
    && mkdir -p /app/data /app/data/ota /app/web \
    && chown -R httpsms:httpsms /app

COPY --from=builder /bin/httpsms-server /app/httpsms-server
COPY --from=dashboard /dashboard/dist/ /app/web/

ENV PORT=8080 \
    DB_PATH=/app/data/httpsms.db \
    OTA_DIR=/app/data/ota \
    WEB_DIR=/app/web

EXPOSE 8080
VOLUME ["/app/data"]

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/v1/health || exit 1

ENTRYPOINT ["/app/httpsms-server"]
