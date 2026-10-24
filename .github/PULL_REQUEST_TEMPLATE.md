## What and why

<!-- What does this change, and why? Link related issues (Fixes #123). -->

## Checklist

- [ ] `make test` passes (gofmt, go vet, race tests)
- [ ] `make dist` still builds every platform
- [ ] Tests cover the change
- [ ] User-visible changes are in `CHANGELOG.md` under `## Unreleased`
- [ ] New or changed tools: routes listed in `Spec.Routes`, risk set and added to `wantRisk`
- [ ] Costly, exposing or destructive actions use the two-step confirmation
- [ ] No secrets returned or logged; customer-chosen text goes in the fenced `Result.Data`
