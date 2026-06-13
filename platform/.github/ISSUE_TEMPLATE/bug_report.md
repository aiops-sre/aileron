---
name: Bug report
about: Something is broken
labels: bug, needs-triage
---

## Describe the bug

<!-- Clear and concise description of what is broken -->

## Steps to reproduce

1.
2.
3.

## Expected behavior

<!-- What should happen -->

## Actual behavior

<!-- What actually happens — include error messages, log lines, screenshots -->

## Environment

- **Component:** (backend / frontend / pipeline / correlation / topology)
- **Cluster:** (your-cluster / local)
- **Version / SHA:** (run `kubectl -n alert-engine-poc get deployment alerthub-backend -o jsonpath='{.spec.template.spec.containers[0].image}'`)

## Relevant logs

```
# kubectl -n alert-engine-poc logs -l app=alerthub-backend --tail=100
```

## Additional context
