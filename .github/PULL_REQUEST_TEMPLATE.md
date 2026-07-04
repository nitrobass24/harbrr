<!-- Title: conventional commit style — feat(scope): …, fix(scope): …, docs(scope): … -->

## Summary

<!-- What changes and why. Link the issue this implements (file one first for anything
     beyond a small fix — see CONTRIBUTING.md). -->

## Testing

<!-- Check what you ran; leave unchecked what doesn't apply. -->

- [ ] `make precommit` (gofumpt + golangci-lint + `go test -race`) — required for any Go change
- [ ] `make build`
- [ ] `make web-ci` (frozen install, type-aware lint, vitest, route-tree drift, build) — required for any `web/` change
- [ ] `make test-openapi` — required when adding/moving an HTTP route (spec updated too)
- [ ] Manual verification (describe below)

<!-- Manual testing notes / screenshots for UI changes: -->

## Checklist

- [ ] Tests accompany the change (table-driven, beside the code)
- [ ] No hand-edits under `internal/indexer/definitions/vendor/` (byte-for-byte from Jackett)
- [ ] No secrets in code, fixtures, or logs; secret fields stay redacted end to end
- [ ] No AI attribution / co-author lines in commits or this PR
