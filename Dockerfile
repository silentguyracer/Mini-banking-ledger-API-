# Stage 1: Build binary
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Cache dependency downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Compile static binary with zero CGO dependencies
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/api ./cmd/api

# Stage 2: Minimal runtime image
FROM gcr.io/distroless/static:nonroot

USER nonroot:nonroot
WORKDIR /app

COPY --from=builder /bin/api /app/api

EXPOSE 8080

ENTRYPOINT ["/app/api"]
