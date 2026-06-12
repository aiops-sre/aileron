FROM python:3.11-slim

ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG NO_PROXY=*.example.com,ghcr.io/aileron-platform,github.com/aileron-platform

ENV HTTP_PROXY=${HTTP_PROXY} \
    HTTPS_PROXY=${HTTPS_PROXY} \
    NO_PROXY=${NO_PROXY}

WORKDIR /app

RUN apt-get update && apt-get install -y \
    gcc \
    g++ \
    curl \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir flask sentence-transformers && \
    pip install --no-cache-dir torch --extra-index-url https://download.pytorch.org/whl/cpu

# Pre-download model at build time — container never needs internet access at runtime
ENV SENTENCE_TRANSFORMERS_HOME=/app/models
RUN python -c "from sentence_transformers import SentenceTransformer; SentenceTransformer('all-MiniLM-L6-v2'); print('Model cached')"

COPY bert_service.py .

RUN adduser --disabled-password --gecos '' appuser && \
    chown -R appuser:appuser /app
USER appuser

# Prevent any runtime downloads
ENV TRANSFORMERS_OFFLINE=1
ENV HF_DATASETS_OFFLINE=1
ENV PORT=8766

HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD curl -f http://localhost:8766/health || exit 1

EXPOSE 8766

CMD ["python", "bert_service.py"]
