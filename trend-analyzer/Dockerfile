# syntax=docker/dockerfile:1

# Builder stage
FROM golang:1.24.0-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

WORKDIR /app/cmd/trend-analyzer
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /analyzer main.go

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

COPY --from=builder /analyzer /analyzer

WORKDIR /

USER appuser

EXPOSE 8080

CMD ["/analyzer"]