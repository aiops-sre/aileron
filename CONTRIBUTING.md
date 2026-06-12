# Contributing to Aileron

Thank you for your interest in contributing to Aileron. This document describes how to set up a local development environment, run tests, and submit changes.

---

## Development Setup

### Prerequisites

| Tool | Minimum version |
|---|---|
| Go | 1.22 (agent/collector), 1.24 (platform/oie) |
| Node.js | 20 LTS |
| Docker + Docker Compose | Docker 24+ / Compose v2 |
| Git | any recent version |

### Clone and start infrastructure

```bash
git clone https://github.com/aileron-platform/aileron.git
cd aileron

# Start all infrastructure dependencies (postgres, redis, kafka, neo4j, ollama)
docker compose up postgres redis zookeeper kafka neo4j ollama

# In a separate terminal, run the platform backend
cd platform && go run ./cmd/alerthub

# In another terminal, run the React frontend in dev mode
cd platform/frontend/alerthub-frontend && npm ci && npm run dev

# Optionally run OIE
cd platform/services/oie && go run ./cmd/oie
```

To start every service at once (built from source):

```bash
docker compose up --build
```

---

## Running Tests

### Go (platform + OIE)

```bash
cd platform && go test ./... -race -timeout 120s
cd platform/services/oie && go test ./... -race -timeout 120s
```

### Go (agent)

```bash
cd agent && go test ./... -race -timeout 120s
```

### Frontend

```bash
cd platform/frontend/alerthub-frontend && npm ci && npm test -- --watchAll=false
```

### All at once (via Makefile)

```bash
make test
```

---

## Code Style

### Go

- All Go code must be formatted with `gofmt` (enforced by CI).
- Run `go vet ./...` before opening a PR; the CI job will fail otherwise.
- Follow standard Go idioms. Prefer short, named return values only when they genuinely aid readability.
- Error strings must be lower-case and not end with punctuation (per the Go style guide).
- New packages must include at least a one-line `doc.go` or package comment.

### TypeScript / React

- The frontend uses ESLint with the project's `.eslintrc` config. Run `npm run lint` before committing.
- Inline styles only — no Tailwind for new code (the design system uses CSS variable tokens via the `c` object).
- Components should be functional with hooks; class components will not be accepted.

---

## Commit Messages

Aileron uses [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short summary>

[optional body]

[optional footer]
```

**Types:** `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`, `ci`

**Examples:**

```
feat(correlation): add temporal decay weight to CACIE fusion
fix(oie): prevent groundless LLM claim when fetcher returns empty
docs(readme): add Okta OIDC setup example
```

Keep the summary line under 72 characters. Use the body to explain *why*, not *what*.

---

## Pull Request Process

1. Fork the repository and create a branch from `main`:
   ```bash
   git checkout -b feat/my-feature
   ```

2. Make your changes, add tests, and ensure all existing tests pass:
   ```bash
   make test
   ```

3. Push your branch and open a Pull Request against `main`.

4. Fill in the PR template — describe what changed, why, and how it was tested.

5. At least one maintainer review is required before merging.

6. PRs are squash-merged to keep the `main` history linear.

### What makes a good PR

- Focused on a single concern. If you find yourself writing "and also..." in the description, split into multiple PRs.
- Includes tests for new behavior. Bug fixes should include a regression test.
- Does not introduce new external dependencies without prior discussion in an issue.
- CI passes — all three test jobs (platform, agent, frontend) must be green.

---

## Reporting Issues

Open a [GitHub Issue](https://github.com/aileron-platform/aileron/issues) and use the appropriate template (bug report or feature request). For security vulnerabilities, follow the process in [SECURITY.md](SECURITY.md) instead.

---

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
