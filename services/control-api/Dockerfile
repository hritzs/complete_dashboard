# Dockerfile for the control-api service

# --- Build Stage ---
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go.mod and go.sum files to download dependencies
COPY go.mod ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application for a minimal, static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /server ./cmd/main.go

# --- Final Stage ---
FROM alpine:latest

# Copy the built binary from the builder stage
COPY --from=builder /server /server

# Expose the port the server listens on
EXPOSE 8080

# Set the entrypoint for the container to run the server
ENTRYPOINT ["/server"]