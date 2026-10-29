FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /zeroconf .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /zeroconf /zeroconf
ENTRYPOINT ["/zeroconf"]
