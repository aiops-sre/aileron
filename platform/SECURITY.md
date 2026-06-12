# Security Policy

## Supported versions

| Version | Supported |
|---|---|
| v1.0.x (current) | Yes |
| < v1.0 | No |

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Report privately via one of:

- **Slack:** DM `@vk-patha` or `#help-interactive-sre`
- **Email:** aileron-admins@group.example.com

Include in your report:
1. Component affected (backend / frontend / pipeline / auth / infra)
2. Description of the vulnerability and potential impact
3. Steps to reproduce (if safe to share)
4. Suggested fix (optional)

You will receive an acknowledgement within **1 business day** and a resolution timeline within **3 business days** for critical issues.

## Security architecture

- All inter-service communication is encrypted in transit (mTLS via Istio)
- Authentication via Apple IdMS / MAS OAuth2 with automatic token refresh
- RBAC enforced at the API layer: Admin / SRE / Operator / Viewer roles
- Secrets managed via Kubernetes secrets (`alerthub-secrets`) — never in code or git history
- Audit logging enabled for all mutating API operations
- Secret scanning runs on every push and PR (see `.github/workflows/secret-scan.yml`)

## Known security controls in CI

| Control | Workflow | When |
|---|---|---|
| Secret detection (gitleaks) | `secret-scan.yml` | Every push + PR |
| Hardcoded credential patterns | `secret-scan.yml` | Every push + PR |
| Large binary detection | `secret-scan.yml` | Every push + PR |
| Go vulnerability check (govulncheck) | `ci-cd.yml` | Every push + PR |
| npm audit | `ci-cd.yml` | Every push + PR |
| Trivy filesystem scan | `ci-cd.yml` | Every push + PR |
| Dependency auto-updates | `dependabot.yml` | Weekly (Monday) |
