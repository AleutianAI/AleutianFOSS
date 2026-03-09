# Embedding Server (Deprecated)

> **Status: DEPRECATED** - This service is no longer used by default.
> Aleutian now uses host Ollama for embeddings (nomic-embed-text-v2-moe).

## Why Deprecated?

The containerized embedding server has been replaced with host Ollama for several reasons:

1. **GPU Access**: Host Ollama has direct MetalKit/CUDA access; containers have limited GPU passthrough
2. **Simplified Stack**: One less container to manage; Ollama already runs for LLM inference
3. **Model Management**: Ollama handles model downloads and updates automatically

## When to Use This Server

This server is still useful for advanced users who need:

- **Gated HuggingFace Models**: EmbeddingGemma, Qwen embeddings (require HF login)
- **Custom Embedding Models**: Fine-tuned models not available in Ollama
- **Specific Pooling Strategies**: This server supports mean pooling, last-token pooling

## Manual Usage

If you need to use this server instead of Ollama:

### 1. Build and Run Manually

```bash
cd services/embeddings
podman build -t aleutian-embeddings .
podman run -d \
  --name aleutian-embeddings \
  -p 12126:8000 \
  -e MODEL_NAME=BAAI/bge-small-en-v1.5 \
  -v ~/.cache/huggingface:/root/.cache/huggingface \
  aleutian-embeddings
```

### 2. For Gated Models (EmbeddingGemma)

```bash
# Create HF token secret first
podman secret create aleutian_hf_token ~/.hf_token

podman run -d \
  --name aleutian-embeddings \
  -p 12126:8000 \
  -e MODEL_NAME=google/embeddinggemma-300m \
  --secret aleutian_hf_token \
  aleutian-embeddings
```

### 3. Update Environment Variables

Point services to this server instead of Ollama:

```bash
# In podman-compose.yml or environment
EMBEDDING_SERVICE_URL=http://localhost:12126/batch_embed
```

Note: The API format differs from Ollama:
- This server: `POST /batch_embed` with `{"texts": ["..."]}`
- Ollama: `POST /api/embed` with `{"model": "...", "input": ["..."]}`

You'll need to modify the orchestrator and RAG engine to use the old format.

## Supported Models

| Model | Pooling | Notes |
|-------|---------|-------|
| `BAAI/bge-small-en-v1.5` | Mean | Default, MIT license |
| `BAAI/bge-base-en-v1.5` | Mean | Larger, better quality |
| `google/embeddinggemma-300m` | Mean | Gated, requires HF login |
| `Qwen/*` | Last-token | Experimental support |

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/embed` | POST | Single text embedding |
| `/batch_embed` | POST | Batch text embeddings |
| `/tokenize` | POST | Token count for text |
| `/health` | GET | Health check |

## Migration to Ollama

To migrate back to the default Ollama-based embeddings:

1. Stop this container: `podman stop aleutian-embeddings`
2. Ensure Ollama has the model: `ollama pull nomic-embed-text-v2-moe`
3. Update `EMBEDDING_SERVICE_URL` to `http://host.containers.internal:11434/api/embed`
4. Restart the stack: `aleutian stack restart`
