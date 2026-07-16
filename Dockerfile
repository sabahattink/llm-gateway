FROM golang:1.25.12-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /llm-gateway ./cmd/gateway

FROM alpine:3.20
RUN apk add --no-cache ca-certificates

COPY --from=builder /llm-gateway /usr/local/bin/llm-gateway
COPY --from=builder /app/web /usr/local/bin/web

WORKDIR /usr/local/bin
EXPOSE 8080
VOLUME /data

ENV DB_PATH=/data/gateway.db
ENTRYPOINT ["llm-gateway"]
