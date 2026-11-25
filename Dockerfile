FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
ENV CGO_ENABLED=0
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -ldflags="-s -w" -o main ./cmd/server

# Test stage
FROM builder AS tester
RUN go test -v -coverprofile=coverage.out ./...
RUN go tool cover -func=coverage.out

FROM alpine:3.23 AS runner
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=builder /app/main .
USER appuser
EXPOSE 8080
ENTRYPOINT ["./main"]
