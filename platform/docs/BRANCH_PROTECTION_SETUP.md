# GitHub Enterprise Branch Protection — Manual Setup Checklist

The API requires SAML SSO so these settings must be applied via the web UI.
A repo admin must do this once. Steps are exact for GitHub Enterprise.

---

## URL

```
https://github.com/aileron-platform/aileron/settings/branches
```

---

## 1. Protect `master`

Click **"Add branch protection rule"** and set:

| Setting | Value |
|---|---|
| Branch name pattern | `master` |
| Require a pull request before merging | ✅ enabled |
| → Required approvals | **1** (increase to 2 for critical changes) |
| → Dismiss stale pull request approvals when new commits are pushed | ✅ enabled |
| → Require review from Code Owners | ✅ enabled (enforces CODEOWNERS) |
| Require status checks to pass before merging | ✅ enabled |
| → Require branches to be up to date before merging | ✅ enabled |
| → Required status checks (add each): | `Frontend CI` `Backend CI` `Validate commit messages` `Validate branch name` `Detect secrets (gitleaks)` `Detect hardcoded patterns` `Block large binaries` |
| Require conversation resolution before merging | ✅ enabled |
| Require signed commits | ✅ enabled (if team has GPG keys set up) |
| Do not allow bypassing the above settings | ✅ enabled |
| Allow force pushes | ❌ disabled |
| Allow deletions | ❌ disabled |

---

## 2. Protect `develop`

Same rule as `master` but:
- Required approvals: **1**
- Require signed commits: optional

---

## 3. General repository settings

Go to `Settings → General`:

| Setting | Value |
|---|---|
| Default branch | `master` |
| Allow merge commits | ✅ (for release merges) |
| Allow squash merging | ✅ (default for feature PRs) |
| Allow rebase merging | ❌ disabled (avoids non-linear history confusion) |
| Automatically delete head branches | ✅ enabled |
| Allow auto-merge | ✅ enabled (for dependabot PRs) |

---

## 4. Security & analysis settings

Go to `Settings → Code security and analysis`:

| Feature | State |
|---|---|
| Dependency graph | ✅ enabled |
| Dependabot alerts | ✅ enabled |
| Dependabot security updates | ✅ enabled |
| Secret scanning | ✅ enabled |
| Secret scanning push protection | ✅ enabled |
| Code scanning (if available on GHE) | ✅ enabled |

---

## 5. Collaborators & teams

Go to `Settings → Collaborators and teams`:

| Team | Role |
|---|---|
| `aiops-sre/sre-backend` | Write |
| `aiops-sre/sre-frontend` | Write |
| `aiops-sre/sre-devops` | Write |
| Repo owner (`vk-patha`) | Admin |

---

## 6. Verify

After applying:
1. Try `git push origin master` directly — should be rejected
2. Open a test PR from a branch named `test-branch` — branch-naming CI should fail
3. Open a PR with commit message `bad message` — commit-lint CI should fail
4. Merge a valid PR — confirm ArgoCD picks it up and deploys

---

## Status checks reference

These are the exact job names from the workflow files that must be listed in step 1:

| Job name | Workflow file |
|---|---|
| `Frontend CI` | `ci-cd.yml` |
| `Backend CI` | `ci-cd.yml` |
| `Validate commit messages` | `commit-lint.yml` |
| `Validate branch name` | `branch-naming.yml` |
| `Detect secrets (gitleaks)` | `secret-scan.yml` |
| `Detect hardcoded patterns` | `secret-scan.yml` |
| `Block large binaries` | `secret-scan.yml` |
