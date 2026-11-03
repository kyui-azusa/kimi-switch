# What changes for the user

<!-- Describe the user-visible effect, not only the code change. Link the issue: Closes #123 -->

## How it was verified

<!-- Commands you ran, and what you exercised manually in the app. -->

## Checklist

- [ ] `go vet ./...` and `go test ./...` pass
- [ ] `pnpm build` and `pnpm test:update-manifest` pass
- [ ] `cargo fmt --all --check` and `cargo clippy --locked --all-targets -- -D warnings` pass in `src-tauri/`
- [ ] New behavior has a test
- [ ] User-visible change: `src/lib/changelog.ts` and `CHANGELOG.md` both updated
- [ ] README change: `README.zh-CN.md` updated to match
- [ ] No API keys, signing keys, or personal paths in the diff
