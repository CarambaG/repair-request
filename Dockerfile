FROM golang:1.25-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -H appuser && mkdir -p /app/uploads && chown -R appuser:appuser /app/uploads
WORKDIR /app
COPY --from=builder /out/server /app/server
COPY web /app/web
USER appuser
EXPOSE 8080
CMD ["/app/server"]
