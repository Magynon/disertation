# -------------------------------
# Build stage
# -------------------------------
FROM golang:1.24.0-bookworm AS builder

# Install Tesseract dependencies
RUN apt-get update -qq && \
    apt-get install -y -qq libtesseract-dev libleptonica-dev tesseract-ocr-eng && \
    rm -rf /var/lib/apt/lists/*

# Set workdir
WORKDIR /app

# Copy and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build your Go binary
RUN CGO_ENABLED=1 go build -o file-parser .

# -------------------------------
# Runtime stage
# -------------------------------
FROM debian:bookworm-slim

# Install runtime dependencies only
RUN apt-get update -qq && \
    apt-get install -y -qq libtesseract-dev libleptonica-dev tesseract-ocr-eng && \
    rm -rf /var/lib/apt/lists/*

# Set the tessdata prefix (where language files are)
ENV TESSDATA_PREFIX=/usr/share/tesseract-ocr/5/tessdata/

# Copy binary from builder
WORKDIR /app
COPY --from=builder /app/file-parser .

# Expose port (optional, if your app has HTTP API)
EXPOSE 8080

# Run
CMD ["./file-parser"]