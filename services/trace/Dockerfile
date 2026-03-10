# Aleutian Trace Server - Multi-stage build
# AST-powered code intelligence with LLM agent capabilities.
#
# GR-75: Three-stage build:
#   Stage 1 (builder)     - Compiles Go binaries with CGO for tree-sitter
#   Stage 2 (lsp-builder) - Installs language servers (pyright, typescript-language-server)
#   Stage 3 (final)       - Minimal Alpine with binaries + language server runtimes
#
# Image size budget: ~170MB compressed (~250MB uncompressed)
# Without language servers: ~35-40MB compressed

# --- Stage 1: Go Builder ---
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

# --- Stage 2: Language Server Builder ---
# GR-75: Install language servers in a separate stage to keep pip/npm caches
# out of the final image. Only the installed binaries are copied forward.
FROM alpine:3.19 AS lsp-builder

RUN apk add --no-cache python3 py3-pip nodejs npm

# Install Pyright (Python type checker / language server).
# Pin version for reproducible builds. --break-system-packages required for
# Alpine 3.19 PEP 668 compliance.
RUN pip3 install --no-cache-dir --break-system-packages pyright==1.1.394
# Verify binary exists
RUN pyright-langserver --version || pyright --version

# Install TypeScript language server.
# Pin versions for reproducible builds. --no-optional skips dev dependencies.
RUN npm install -g --no-optional typescript@5.7 typescript-language-server@4.3
# Verify binary exists
RUN typescript-language-server --version

# --- Stage 3: Final Image ---
FROM alpine:3.19
WORKDIR /app

# Runtime dependencies: curl for health checks, python3/nodejs for language servers.
# No pip or npm in final image — only the runtimes needed to execute installed servers.
RUN apk add --no-cache curl python3 nodejs

# Copy Go binaries from builder
COPY --from=builder /app/trace .
COPY --from=builder /app/trace-proxy .

# Copy Pyright from lsp-builder.
# Pyright installs as a Python package with a console_scripts entry point.
COPY --from=lsp-builder /usr/bin/pyright-langserver /usr/bin/pyright-langserver
COPY --from=lsp-builder /usr/lib/python3.11/site-packages/pyright /usr/lib/python3.11/site-packages/pyright

# Copy TypeScript language server from lsp-builder.
# npm global installs go to /usr/local/lib/node_modules with symlinks in /usr/local/bin.
COPY --from=lsp-builder /usr/local/lib/node_modules /usr/local/lib/node_modules
COPY --from=lsp-builder /usr/local/bin/typescript-language-server /usr/local/bin/typescript-language-server
COPY --from=lsp-builder /usr/local/bin/tsserver /usr/local/bin/tsserver

# Ensure language server binaries are in PATH
ENV PATH="/usr/local/bin:/usr/bin:${PATH}"

# GR-75: LSP defaults for container mode.
# These enable LSP enrichment automatically when running in the container.
# CLI mode (brew install) does not set these, keeping zero dependencies.
ENV LSP_ENABLED=true
ENV LSP_PYTHON_ENABLED=true
ENV LSP_TYPESCRIPT_ENABLED=true
ENV LSP_STARTUP_TIMEOUT_SECONDS=30
ENV LSP_REQUEST_TIMEOUT_SECONDS=5

EXPOSE 12217
CMD ["/app/trace", "-with-context", "-with-tools", "-lsp-enabled"]
