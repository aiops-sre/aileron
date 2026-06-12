#!/bin/bash
set -e

MODELS_READY=/ollama-models/.models-registered

# On first startup only: register pre-downloaded GGUF models into Ollama's blob store.
# OLLAMA_MODELS is a PVC so the blobs persist across pod restarts.
if [ ! -f "$MODELS_READY" ] && [ -f /ollama-models-raw/qwen2.5-3b.gguf ]; then
  echo "First start: registering pre-downloaded GGUF models..."

  /bin/ollama serve > /tmp/ollama-init.log 2>&1 &
  SERVER_PID=$!

  for i in $(seq 1 60); do
    sleep 2
    if /bin/ollama list > /dev/null 2>&1; then
      echo "Ollama ready after $((i*2))s"; break
    fi
    if [ "$i" -eq 60 ]; then
      echo "ERROR: Ollama server failed to start during init"
      cat /tmp/ollama-init.log
      kill $SERVER_PID 2>/dev/null || true
      exit 1
    fi
  done

  /bin/ollama create qwen2.5:3b -f /Modelfile-qwen \
    && /bin/ollama create nomic-embed-text -f /Modelfile-nomic \
    && touch "$MODELS_READY" \
    && echo "Models registered successfully"

  kill $SERVER_PID 2>/dev/null || true
  sleep 2
fi

exec /bin/ollama serve
