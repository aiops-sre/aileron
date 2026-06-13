# Contributing to AlertHub Enterprise

## Setup

```bash
git clone git@github.com/aileron-platform:aiops-sre/alert-engine.git
cd alert-engine

# Install git hooks (do this once after cloning)
bash scripts/setup-git-hooks.sh
```

## Branch strategy

| Branch | Purpose | Direct push |
|---|---|---|
| `master` | Production — deployed automatically via ArgoCD | **Never** |
| `develop` | Integration branch for next release | Maintainers only |
| `feat/*` | New features | Your fork |
| `fix/*` | Bug fixes | Your fork |
| `hotfix/*` | Critical production fixes | Requires immediate PR |
| `chore/*` | Dependency bumps, tooling, CI | Your fork |
| `docs/*` | Documentation only | Your fork |
| `release/*` | Release prep (e.g. `release/v1.3.0`) | Maintainers only |

**Never push directly to `master`.** Always open a PR from a feature branch.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
type(scope): short description (≤ 100 chars)

Optional body explaining WHY (not what — the code shows what).
```

**Types:** `feat` `fix` `docs` `chore` `refactor` `test` `perf` `ci` `build` `style` `revert`

**Scopes for this project:** `pipeline` `correlation` `topology` `api` `frontend` `auth` `infra` `deps` `alerts` `incidents` `db`

**Breaking change:** append `!` → `feat(api)!: change alert schema`

Good examples:
```
feat(pipeline): add temporal burst grouping for alert storms
fix(correlation): correct topology authority override threshold
chore(deps): bump github.com/lib/pq to v1.10.9
ci: fix BuildKit context URL after repo migration
docs: update correlation engine weights in README
```

The `commit-msg` hook enforces this format locally. CI enforces it on every PR.

## Pull requests

1. Open a PR against `master` (or `develop` for non-urgent changes)
2. Fill in the PR template — link the issue or Slack thread
3. The CI suite must pass: `frontend-ci`, `backend-ci`, `commit-lint`, `branch-naming`, `secret-scan`
4. CODEOWNERS rules require approval from the relevant team before merge
5. Squash-merge preferred for feature branches; merge commit for releases

## Code style

**Backend (Go):**
- `gofmt` + `goimports` required (CI will flag violations via `golangci-lint`)
- No `fmt.Println` in committed code — use structured logging (`log.Printf`)
- Prefer `context.Context` propagation; no `context.Background()` in handlers

**Frontend (TypeScript/React):**
- Inline styles only — `c` token object with CSS variables, no Tailwind classes
- API responses: always `r.data?.data ?? r.data` before `toArr()` (backend double-wraps)
- No `console.log` in committed code

## Database migrations

- All schema changes go in `/internal/db/` as numbered migration files
- Migrations must be **reversible** (include a `down` migration)
- Never modify an existing migration — always add a new one
- Test locally: `DATABASE_URL=... go run cmd/migrate/main.go up`

## What not to commit

- `.env` files (use `.env.example` as a template)
- Compiled binaries (`alerthub_linux_amd64`, etc.) — the build pipeline handles this
- Files >5 MB — use the artifact store
- Secrets, API keys, or tokens of any kind

The `pre-commit` hook blocks these automatically. If it fires, it's correct.

## Deployment

Deployment is fully automated:

```bash
git push origin feat/my-feature   # push branch
# open PR → CI runs → get approval → merge to master
# ArgoCD detects the push to master and deploys automatically
```

No manual `docker build` or `kubectl apply` steps. See `README.md` for details.
