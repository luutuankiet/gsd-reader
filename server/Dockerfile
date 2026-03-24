# Stage 1: Pull reader dist/ assets from npm
FROM node:20-alpine AS assets
WORKDIR /tmp
RUN npm pack @luutuankiet/gsd-reader@latest && \
    tar xzf luutuankiet-gsd-reader-*.tgz

# Stage 2: Build Go binary
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o gsd-reader main.go

# Stage 3: Runtime
FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /build/gsd-reader .
COPY --from=assets /tmp/package/dist ./dist-template

# Create data directory
RUN mkdir -p /data

EXPOSE 8080

CMD ["./gsd-reader"]
