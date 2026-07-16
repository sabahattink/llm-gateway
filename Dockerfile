FROM golang:1.26.5-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /llm-gateway ./cmd/gateway

FROM alpine:3.24.1
RUN apk add --no-cache ca-certificates \
    && addgroup -S gateway \
    && adduser -S -G gateway gateway \
    && install -d -o gateway -g gateway -m 0700 /data

COPY --from=builder /llm-gateway /usr/local/bin/llm-gateway
COPY --from=builder /app/web /usr/local/bin/web

WORKDIR /usr/local/bin
EXPOSE 8080
VOLUME /data

ENV DB_PATH=/data/gateway.db
USER gateway:gateway
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["llm-gateway"]
