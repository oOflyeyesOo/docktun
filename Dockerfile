# ==============================================================================
# Stage 1: Build Vue 3 Frontend Assets
# ==============================================================================
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend-builder
WORKDIR /app
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN corepack enable && corepack prepare pnpm@11.25.0 --activate && pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm run build

# ==============================================================================
# Stage 2: Compile Pure Go Backend Server
# ==============================================================================
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS backend-builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /server ./cmd/server
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /template-helper ./cmd/template-helper
COPY zeroconf/ /zeroconf-src/
RUN cd /zeroconf-src && go test ./... && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w" -o /zeroconf .

# ==============================================================================
# Stage 3: Unified DockTun Minimal Runtime (No Nginx, Pure Go + Alpine)
# ==============================================================================
# Rebuild the pinned Cosign source with the patched compiler and HTML parser.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS signature-verifier
ARG TARGETOS
ARG TARGETARCH
WORKDIR /cosign
RUN go mod download github.com/sigstore/cosign/v3@v3.1.4 && \
    cp -R /go/pkg/mod/github.com/sigstore/cosign/v3@v3.1.4/. . && \
    chmod -R u+w . && \
    go get golang.org/x/net@v0.62.0 && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /cosign-bin ./cmd/cosign
FROM alpine:3.23
RUN apk --no-cache add ca-certificates docker-cli docker-cli-compose docker-cli-buildx git tzdata curl && \
    git config --global --add safe.directory '*'

COPY --from=signature-verifier /cosign-bin /usr/local/bin/cosign
WORKDIR /app

# Copy compiled backend binary and built frontend SPA assets
COPY --from=backend-builder /server /app/server
COPY --from=backend-builder /template-helper /app/template-helper
COPY --from=backend-builder /zeroconf /app/zeroconf
COPY --from=frontend-builder /app/dist /app/dist

# Prepare directories
RUN mkdir -p /app/data /etc/cloudflared /opt/projects

ENV STATIC_DIR=/app/dist
ENV LISTEN_ADDR=0.0.0.0:3001
ENV DB_PATH=/app/data/app.db

EXPOSE 3001 80

ENTRYPOINT ["/app/server"]
