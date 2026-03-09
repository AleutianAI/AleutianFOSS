# Aleutian Trace Server - Multi-stage build
# AST-powered code intelligence with LLM agent capabilities.

# --- Build Stage ---
FROM golang:1.25-alpine AS builder

# go-tree-sitter requires CGO (C bindings to tree-sitter parsers).
RUN apk add --no-cache gcc g++ musl-dev

WORKDIR /src

# Copy Go module files first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY services/ ./services/
COPY pkg/ ./pkg/
COPY cmd/ ./cmd/

# Build the trace binary (CGO required for go-tree-sitter).
# Static link so the final stage doesn't need gcc/g++ runtime libs.
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags '-extldflags "-static"' -o /app/trace ./cmd/trace

# Build trace-proxy (no CGO needed — pure Go HTTP proxy)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags '-extldflags "-static"' -o /app/trace-proxy ./cmd/trace-proxy

# --- Final Stage ---
FROM alpine:latest
WORKDIR /app

RUN apk add --no-cache curl

# Copy compiled binaries from the builder stage
COPY --from=builder /app/trace .
COPY --from=builder /app/trace-proxy .

EXPOSE 12217
CMD ["/app/trace", "-with-context", "-with-tools"]
