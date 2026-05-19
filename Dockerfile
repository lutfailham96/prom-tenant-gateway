FROM golang:1.25-alpine AS builder
WORKDIR /app
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o main main.go

FROM alpine:3.22 AS runner
RUN apk add --no-cache git ca-certificates tzdata && \
    adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=builder /app/main .
USER appuser
EXPOSE 8080
ENTRYPOINT ["./main"]
