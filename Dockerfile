FROM golang:alpine AS builder

WORKDIR /go/src/github.com/nebed/azure-spot-monitor
RUN apk add --update --no-cache git gcc libc-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o main .

# Final stage: slim alpine image for production
FROM alpine:latest AS prod_base

# Copy compiled binary from builder stage
COPY --from=builder /go/src/github.com/nebed/azure-spot-monitor/main /

# Create unprivileged user in alpine
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid "10001" \
    appuser

# Switch to non-root user
USER appuser:appuser

# Expose service port
EXPOSE 8080

# Run the binary
CMD ["/main"]
