# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
COPY frontend ./frontend
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o server .

# Final stage — minimal image
FROM scratch
COPY --from=builder /app/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
