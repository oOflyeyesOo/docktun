# ==============================================================================
# Stage 1: Build Vue 3 Frontend Assets
# ==============================================================================
FROM node:20-alpine AS frontend-builder
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install
COPY frontend/ ./
RUN npm run build

# ==============================================================================
# Stage 2: Compile Pure Go Backend Server
# ==============================================================================
FROM golang:1.22-alpine AS backend-builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /server ./cmd/server

# ==============================================================================
# Stage 3: Unified DockTun Minimal Runtime (No Nginx, Pure Go + Alpine)
# ==============================================================================
FROM alpine:3.19
RUN apk --no-cache add ca-certificates docker-cli docker-cli-compose git tzdata curl && \
    git config --global --add safe.directory '*'

WORKDIR /app

# Copy compiled backend binary and built frontend SPA assets
COPY --from=backend-builder /server /app/server
COPY --from=frontend-builder /app/dist /app/dist

# Prepare directories
RUN mkdir -p /app/data /etc/cloudflared /opt/projects

ENV STATIC_DIR=/app/dist
ENV LISTEN_ADDR=0.0.0.0:3001
ENV DB_PATH=/app/data/app.db

EXPOSE 3001 80

ENTRYPOINT ["/app/server"]
