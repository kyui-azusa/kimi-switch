# KimiSwitch

KimiSwitch is a Tauri desktop app and local-only Kimi Code API-key pool for personal or authorized internal use.

Its desktop information architecture, visual tokens, and selected UI wrappers are adapted from [CC Switch](https://github.com/farion1231/cc-switch) under the MIT License to keep the workflow familiar. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). KimiSwitch is not affiliated with Moonshot AI, Kimi, or the CC Switch project.

Project behavior and design decisions are recorded in [`docs/`](docs/README.md).
Updater signing, release-server deployment, and rollback are documented in [`docs/updates.md`](docs/updates.md).

## What it does

- Runs a loopback proxy on `127.0.0.1:15722`, avoiding CC Switch's default `15721` port.
- Keeps Kimi API keys in environment variables or the OS credential store (macOS Keychain / Windows Credential Manager) instead of command-line arguments.
- Supports manual active-key switching.
- Supports an optional automatic rotation mode:
  - `sticky`: keeps using the current key and only fails over when quota is exhausted, authentication fails, rate limits hit, or the upstream becomes unavailable.
- Retries a request on an upstream 401, 429, or 5xx response only when rotation is enabled and no successful stream has started.
- Puts rate-limited keys into a cooldown window.
- Provides per-account and test-all availability checks using a one-output-token completion, so authentication-only success cannot hide an exhausted quota.
- Classifies observed failures as exhausted quota, temporary rate limit, invalid key, disabled account, network failure, or upstream failure using HTTP status, provider error type, and message text.
- Quarantines failed accounts for at least 10 minutes. Healthy accounts are always preferred; a cooled account is retried only when no enabled healthy account remains.
- Records per-key and per-day request, success, error, input, output, cache-read, reasoning, observed-total, and estimated-token statistics.
- Phase 1 supports manually recording quota refresh reference times for the five-hour window and weekly window. These values are informational only and never drive rotation, health, or quota decisions.
- Provides a separate Kimi Code direct/KimiSwitch takeover switch. Takeover changes only model-provider routing, preserves the original provider, creates `~/.kimi-code/config.toml.kimiswitch.bak`, and can restore direct mode atomically.
- Does not scrape undocumented quota pages, spoof the client identity, or claim that locally observed usage is an account's remaining quota.

## Quick start

Desktop development:

```sh
cd /Users/azusa/dev/open-source/kimi-switch
pnpm install
pnpm tauri:dev
```

The Tauri app builds and starts the Go proxy as a bundled sidecar. The CLI remains available for scripting:

```sh
cd /Users/azusa/dev/open-source/kimi-switch
go run ./cmd/kimi-switch init
go run ./cmd/kimi-switch add work --env KIMI_WORK_KEY
go run ./cmd/kimi-switch add personal --env KIMI_PERSONAL_KEY
export KIMI_WORK_KEY='your-work-key'
export KIMI_PERSONAL_KEY='your-personal-key'
go run ./cmd/kimi-switch rotation on sticky
go run ./cmd/kimi-switch serve
```

## Desktop installers

Build the macOS disk image locally:

```sh
pnpm tauri build --bundles dmg
```

The bundled app and installer show the original KimiSwitch icon and the attribution `Powered by kyuiazusa`. The icon uses a white sideways-pi switch mark with a small green status element; it is not a Kimi or CC Switch product mark.

The GitHub Actions workflow in `.github/workflows/build-installers.yml` builds signed Tauri updater artifacts for macOS Apple Silicon, macOS Intel, and Windows x86_64 on version tags such as `v0.2.0`. It publishes the platform files and a validated `latest.json` only after every build succeeds. Updater signatures are separate from Apple Developer ID/notarization and Windows Authenticode installer signing.

Version `0.2.0` is the first updater-enabled release. Existing `0.1.x` users must install it manually once; later releases can be checked, downloaded, installed, and restarted from the settings page.

For system credential storage:

```sh
go run ./cmd/kimi-switch secret-set kimi-switch/work
go run ./cmd/kimi-switch add work --secret-ref kimi-switch/work
```

The proxy expects the client to send the local proxy key configured as `local_api_key`. The generated default is `local-kimi-switch`.

To generate Kimi Code CLI's temporary model environment variables:

```sh
go run ./cmd/kimi-switch print-config
```

The important values are conceptually:

```sh
export KIMI_MODEL_NAME='kimi-for-coding'
export KIMI_MODEL_API_KEY='local-kimi-switch'
export KIMI_MODEL_BASE_URL='http://127.0.0.1:15722/v1'
kimi
```

The local proxy consumes the client key and injects the selected upstream key. The Kimi Code CLI currently distinguishes API-key direct connections from OAuth-managed endpoints, and its model environment variables support a custom base URL; use the API-key path for this proxy.

## Import existing Kimi providers

KimiSwitch can extract `api_key` from `[providers.<name>]` or `KIMI_API_KEY` from `[providers.<name>.env]` in `~/.kimi-code/config.toml` and copy the values into macOS Keychain or Windows Credential Manager. It does not rewrite or delete the Kimi config:

```sh
go run ./cmd/kimi-switch init
go run ./cmd/kimi-switch import-kimi
```

OAuth-only providers are intentionally skipped. The importer is for API-key providers only.

## Kimi Code takeover switch

The desktop header has two independent switches:

- **Kimi Code direct / KimiSwitch takeover** controls whether models in `~/.kimi-code/config.toml` use their original provider or the local KimiSwitch provider.
- **Automatic rotation** controls whether KimiSwitch keeps the current account sticky and fails over only when that account stops working.

Import remains read-only. Enabling takeover keeps the original provider block intact, writes a marked `kimi-switch-local` provider, redirects only model `provider` references that used the active original provider, and creates a one-time backup. Disabling takeover restores those references and removes the managed block. After either change, run `/reload` in an already-running Kimi Code session; new sessions read the new configuration directly.

The usage page follows the familiar CC Switch breakdown while adapting it to Kimi's response fields: input, output, cache-read, reasoning, total, request outcomes, per-account totals, and 7/30-day trends. Real token values come from upstream `usage`; requests without a usable `usage` object are shown separately as estimates.

## Manual quota refresh references

[#2](https://github.com/kyui-azusa/kimi-switch/issues/2) delivers the manual first phase. [#13](https://github.com/kyui-azusa/kimi-switch/issues/13) tracks official synchronization as a separate second phase.

- Phase 1 stores user-entered reference timestamps for the five-hour window and weekly window.
- The desktop app and legacy web form show these values in local time for easier manual entry.
- The persisted config and status API use UTC RFC3339 timestamps such as `2026-08-21T09:00:00Z`.
- The desktop app shows these values as `手动参考 · 非官方`.
- They are only for arranging real verification tests and observation.
- They must not be treated as authoritative remaining-quota data, and they must not affect rotation, account health, or automatic switching.

The only authoritative source of truth is a value that is directly provided by the official upstream, can be re-checked reliably, and has clear provenance. Manual entries, test observations, derived estimates, `imported_at`, `Retry-After`, and error messages are all reference signals only until such an official field exists and is synchronized.

See [docs/README.md](docs/README.md) and [docs/account-health.md](docs/account-health.md) for the phase-1 wording used by the desktop UI.

## Admin endpoints

All admin endpoints use the same local bearer key as the proxy.

```text
GET  /healthz
GET  /api/status
GET  /api/usage
PUT  /api/kimi/takeover          {"enabled":true}
POST /api/rotation              {"enabled":true,"mode":"sticky"}
POST /api/keys/<name>/use
POST /api/keys/<name>/test
```

The proxy keeps request bodies only in memory while forwarding them. Persistent state contains metadata and usage counters, not prompts or responses.

## Verification

```sh
go test ./...
go vet ./...
```

## License

KimiSwitch is released under the MIT License. The CC Switch project is a separate project with its own license and attribution requirements.
