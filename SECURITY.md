# Security Policy

## Supported Versions

Security fixes are applied to the **latest commit on `main`**. We do not backport fixes to older tagged releases. We recommend always running the most recent image tag (`latest` or the specific SHA published by CI).

| Version | Supported |
|---|---|
| `main` (latest) | Yes |
| Older tagged releases | No — upgrade to latest |

---

## Reporting a Vulnerability

**Please do not open a public GitHub Issue for security vulnerabilities.**

Use [GitHub Private Vulnerability Reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability) to submit a report confidentially:

1. Navigate to the Aileron repository on GitHub.
2. Click **Security** → **Advisories** → **Report a vulnerability**.
3. Fill in the details: affected component, steps to reproduce, potential impact, and (if known) a suggested fix.

If you are unable to use GitHub's private reporting, send an encrypted report to the maintainers via the email address listed in the repository's `CODEOWNERS` file.

---

## Response Timeline

| Milestone | Target |
|---|---|
| Acknowledgement of report | 48 hours |
| Initial assessment and severity triage | 7 days |
| Patch available for critical/high severity | 30 days |
| Patch available for medium/low severity | 90 days |
| Public disclosure (CVE if applicable) | After patch is released |

We will keep you informed at each milestone. If you have a hard deadline (e.g., a conference disclosure), please mention it in your initial report so we can coordinate.

---

## Security Features

The following controls are implemented in Aileron. Understanding them may help scope your research.

**Authentication and authorization**

- JWT Bearer-only authentication for all API endpoints — cookies are not used for API auth, eliminating CSRF attack surface.
- Generic OIDC integration with group-to-role mapping (admin / operator / viewer). Role is resolved server-side from IdP group claims; clients cannot self-elevate.
- All role and permission checks are enforced in middleware before reaching handler logic.

**Transport and web security**

- WebSocket connections validate the `Origin` header against an allowlist; cross-origin WebSocket upgrade attempts are rejected.
- CORS allowlist is explicitly configured; wildcard origins are not permitted in production mode.
- `Content-Security-Policy` and `Strict-Transport-Security` headers are set on all responses.
- Internal service-to-service communication uses shared secret tokens; these are never exposed in API responses.

**Rate limiting and abuse prevention**

- Redis-backed rate limiting is applied per IP and per authenticated user on all inbound webhook and API endpoints.
- Repeated authentication failures trigger exponential backoff at the IdP redirect layer.

**LLM / prompt injection prevention**

- All user-controlled strings inserted into LLM prompts are sanitized and wrapped in explicit delimiters.
- Evidence grounding gates suppress LLM calls when factual support is insufficient, reducing the prompt-injection surface to attacker-controlled alert payloads.
- LLM model endpoints are internal-only (not exposed outside the cluster); Ollama's API is not reachable from the public network.

**Error handling**

- Internal error messages (stack traces, SQL errors, internal service URLs) are never included in HTTP responses returned to clients.
- All error responses use a uniform envelope with a stable error code; implementation details are logged server-side only.

**Data**

- Database credentials, JWT secrets, and OIDC client secrets are consumed from environment variables or Kubernetes Secrets; they are never embedded in source code or container images.
- pgvector embeddings stored in PostgreSQL are tenant-scoped; cross-tenant data access is blocked at the query layer.
