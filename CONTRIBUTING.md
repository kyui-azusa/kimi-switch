# Contributing to KimiSwitch

Thanks for taking the time to help. Issues and pull requests are welcome in English or 中文.

## Before you start

- **Bugs and features** — open an issue first for anything larger than a small fix, so the approach
  can be agreed before you write code.
- **Security problems** — do not open a public issue. Follow [SECURITY.md](SECURITY.md).
- **Scope** — KimiSwitch rotates among accounts you already control. Changes that scrape
  undocumented quota endpoints, spoof client identity, or otherwise try to obtain quota you were
  not granted are out of scope and will be declined.

## Development setup

Requires Node 22, pnpm 10.16, Go 1.24, and a stable Rust toolchain.

```sh
git clone https://github.com/kyui-azusa/kimi-switch.git
cd kimi-switch
pnpm install
pnpm tauri:dev
```

`pnpm tauri:dev` builds the Go proxy as a sidecar binary and starts it alongside the desktop app.
To work on the proxy alone, run it directly:

```sh
go run ./cmd/kimi-switch init
go run ./cmd/kimi-switch serve
```

## Project layout

| Path | Contents |
| --- | --- |
| `cmd/kimi-switch/` | CLI entry point |
| `internal/proxy/` | Loopback proxy, admin API, usage accounting |
| `internal/rotation/` | Account selection, failover, cooldown |
| `internal/config/` | On-disk configuration |
| `internal/secret/` | OS credential store access |
| `internal/kimiimport/`, `internal/kimitakeover/` | Reading and rewriting `~/.kimi-code/config.toml` |
| `src/` | React desktop UI |
| `src-tauri/` | Tauri shell, updater configuration, icons |
| `scripts/` | Sidecar build, update-manifest generation, DMG repair |
| `docs/` | Behavior and design decisions |

## Before opening a pull request

Run the same checks CI runs:

```sh
go vet ./...
go test ./...
pnpm build                 # tsc --noEmit && vite build
pnpm test:update-manifest
cd src-tauri && cargo fmt --all --check && cargo clippy --locked --all-targets -- -D warnings
```

New behavior needs a test. The proxy, rotation, and takeover packages all have table-driven tests
to follow.

## Conventions

- **Branches** — `feat/…`, `fix/…`, `docs/…`, `chore/…`.
- **Commits** — Conventional Commits with an optional scope: `fix(rotation): …`, `feat(updater): …`,
  `docs: …`. Subject lines may be in English or Chinese; match the surrounding history.
- **Pull requests** — one logical change per PR. Explain what changes for the user, not only what
  changed in the code, and say how you verified it.
- **User-visible changes** — update `src/lib/changelog.ts` (the in-app changelog) and
  [CHANGELOG.md](CHANGELOG.md) in the same PR. The two must stay in sync.
- **Documentation** — behavior changes belong in `docs/`. If you change anything in the README,
  update [README.zh-CN.md](README.zh-CN.md) to match.

## Releases

Maintainers only. Keep the version identical in `package.json`, `src-tauri/Cargo.toml`, and
`src-tauri/tauri.conf.json`, then push a matching `v*` tag. The release workflow builds signed
macOS and Windows artifacts and publishes both update manifests. Signing, dual update sources, and
rollback are documented in [docs/updates.md](docs/updates.md).

## License

By contributing, you agree that your contributions are licensed under the repository's
[LICENSE](LICENSE).
