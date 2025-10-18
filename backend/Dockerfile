# Stage 1: Builder for Go application
FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS builder

WORKDIR /app

# Target platform (can be overridden by buildx)
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOTOOLCHAIN=local

# Install minimal deps for Go (git for go mod)
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*

# Copy go.mod and go.sum first to leverage Docker cache
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy the entire backend source code
COPY backend/ ./

# Build the Go application
RUN go build -trimpath -ldflags="-s -w" -o /app/opencoze main.go


# Stage 2: Final image
FROM alpine:3.22.0

WORKDIR /app

# Install runtime deps; use system numpy/pillow to avoid compiling heavy wheels
RUN apk add --no-cache \
    pax-utils python3 py3-numpy py3-pillow \
    bind-tools file curl

# Create venv that can use system site-packages (numpy/pillow)
RUN python3 -m venv --system-site-packages --copies --upgrade-deps /app/.venv && \
    . /app/.venv/bin/activate && \
    pip install --no-cache-dir \
      urllib3==1.26.16 \
      h11==0.16.0 \
      httpx==0.28.1 \
      pdfplumber==0.11.7 \
      python-docx==1.2.0

# Copy the built Go binary from the builder stage
COPY --from=builder /app/opencoze /app/opencoze

# Copy Python application scripts
COPY backend/infra/document/parser/impl/builtin/parse_pdf.py /app/parse_pdf.py
COPY backend/infra/document/parser/impl/builtin/parse_docx.py /app/parse_docx.py
COPY backend/infra/coderunner/impl/script/sandbox.py /app/sandbox.py


# Copy static resources
COPY backend/static /app/resources/static/
COPY backend/conf /app/resources/conf/
COPY docker/.env.example /app/.env
# COPY docker/.env.ve /app/.env
# COPY docker/cert.pem /app/cert.pem
# COPY docker/key.pem /app/key.pem

# Set PATH to prioritize venv's binaries
ENV PATH="/app/.venv/bin:${PATH}"
# ENV LD_LIBRARY_PATH="/usr/lib:${LD_LIBRARY_PATH}" # Keep commented for now

# Ensure python scripts and venv executables are executable
RUN chmod +x /app/parse_pdf.py /app/parse_docx.py  && \
    find /app/.venv/bin -type f -exec chmod +x {} \;


EXPOSE 8888

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

CMD ["/app/opencoze"]
