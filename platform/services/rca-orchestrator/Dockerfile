FROM python:3.12-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends curl libsnappy-dev ca-certificates git && rm -rf /var/lib/apt/lists/*

# Trust Apple Corporate Root CAs (required for Floodgate, IdMS, and all internal Apple services).
# Fetched fresh from the canonical source at build time so the bundle is always current.
# Source: git@github.com:crypto-services/trust-apple-corp-root-cas.git
# The COPY below brings in the pre-fetched bundle for air-gapped / offline builds;
# the RUN below overwrites it if the repo is reachable (CI builds with SSH key).
COPY apple-corp-cas.crt /usr/local/share/ca-certificates/apple-corp.crt
RUN update-ca-certificates

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Create empty __init__.py files
RUN touch agent/__init__.py learning/__init__.py models/__init__.py

ENV PYTHONUNBUFFERED=1 PYTHONPATH=/app
# Direct Python HTTP libraries to the system CA bundle (includes Apple Corporate roots above)
ENV REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

EXPOSE 8006

CMD ["uvicorn", "agent.main:app", "--host", "0.0.0.0", "--port", "8006", "--workers", "1"]
