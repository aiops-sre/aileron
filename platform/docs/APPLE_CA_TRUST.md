# Apple Corporate CA Trust

## Why this exists

Apple-internal services — including `` (the GenAI gateway used for RCA) — present TLS certificates signed by Apple Corporate Root CAs.
These roots are **not** in any public CA bundle, so containers built from public base images will reject the connection with `certificate signed by unknown authority` unless they are explicitly trusted.

Previously the code worked around this with `InsecureSkipVerify: true` (controlled by `INTERNAL_TLS_INSECURE=true`).
That flag is now only permitted on the no-mTLS dev fallback path. Production pods (mTLS cert mounted at `/narrative/kube-actor/cert.pem`) always verify the server certificate.

---

## The CA bundle

**File:** `docker/apple-corp-cas.crt`  
**Source:** `git@github.com:crypto-services/trust-apple-corp-root-cas.git`  
**Support:** [#help-certificatemanager](https://apple.slack.com/app_redirect?channel=help-certificatemanager)

| Common Name | Key | SHA-256 Thumbprint | Expires |
|---|---|---|---|
| Apple Corporate Root CA | RSA 2048 | `50:41:…:7A:D3` | **2029-07-17** |
| Apple Corporate Root CA 2 | EC P-384 | `5E:A8:…:88:CB` | 2036-08-14 |
| Apple Corporate RSA Root CA 3 | RSA 4096 | `B2:EC:…:5F:E5` | 2041-02-13 |

The shortest-lived cert expires **2029-07-17**. Set a calendar reminder for **2028-07** to check the upstream repo for a replacement bundle before that date.

---

## How it is wired into the images

### Go backend (`docker/Dockerfile.backend` — Alpine)

```dockerfile
COPY docker/apple-corp-cas.crt /usr/local/share/ca-certificates/apple-corp.crt
RUN update-ca-certificates
```

Alpine's `update-ca-certificates` appends the certs to `/etc/ssl/certs/ca-certificates.crt`.
Go's `crypto/tls` uses the system CA pool by default when `tls.Config.RootCAs` is `nil`, so no code change is needed after the image is built.

### Python RCA Orchestrator (`services/rca-orchestrator/Dockerfile` — Debian slim)

```dockerfile
COPY apple-corp-cas.crt /usr/local/share/ca-certificates/apple-corp.crt
RUN update-ca-certificates

ENV REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
```

Python's `requests` and most HTTP libraries default to the `certifi` bundle, not the system store.
The two env vars redirect them to the system bundle (which now includes the Apple roots).

---

## How to update the bundle

When Apple rotates a root CA, the upstream repo is updated first. To pick up the new cert:

```bash
# 1. Pull the latest script (contains the embedded PEM certs)
git clone --depth 1 git@github.com:crypto-services/trust-apple-corp-root-cas.git /tmp/trust-apple-corp-root-cas

# 2. Dump the current corporate roots into a new bundle
bash /tmp/trust-apple-corp-root-cas/trust_apple_corp_root_cas.sh --ca-dump - > docker/apple-corp-cas.crt

# 3. Sync to the rca-orchestrator build context
cp docker/apple-corp-cas.crt services/rca-orchestrator/apple-corp-cas.crt

# 4. Rebuild and redeploy both images
```

---

## CI expiry check

Add this step to your pipeline to get an early warning before a cert expires:

```bash
#!/usr/bin/env bash
# Fail if any cert in the bundle expires within 365 days.
set -euo pipefail
cert=""
while IFS= read -r line; do
  cert+="$line"$'\n'
  if [[ "$line" == "-----END CERTIFICATE-----" ]]; then
    subject=$(echo "$cert" | openssl x509 -noout -subject 2>/dev/null | sed 's/subject=//')
    enddate=$(echo "$cert" | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
    # macOS-compatible date parsing
    if date --version >/dev/null 2>&1; then
      exp_epoch=$(date -d "$enddate" +%s)   # GNU date (Linux)
    else
      exp_epoch=$(date -jf "%b %d %T %Y %Z" "$enddate" +%s)  # BSD date (macOS)
    fi
    days=$(( (exp_epoch - $(date +%s)) / 86400 ))
    if [[ $days -lt 365 ]]; then
      echo "ERROR: '$subject' expires in ${days} days ($enddate) — update docker/apple-corp-cas.crt"
      exit 1
    else
      echo "OK: '$subject' — ${days} days remaining"
    fi
    cert=""
  fi
done < docker/apple-corp-cas.crt
```

---

## Relevant code

| File | What changed |
|---|---|
| `internal/services/floodgate/floodgate.go` | `InsecureSkipVerify` removed from mTLS path; scoped to no-cert fallback only |
| `docker/apple-corp-cas.crt` | Embedded CA bundle (backend build context) |
| `services/rca-orchestrator/apple-corp-cas.crt` | Same bundle (orchestrator build context) |
| `docker/Dockerfile.backend` | Installs bundle into Alpine system trust store |
| `services/rca-orchestrator/Dockerfile` | Installs bundle + sets `REQUESTS_CA_BUNDLE` / `SSL_CERT_FILE` |
