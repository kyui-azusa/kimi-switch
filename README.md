# KimiSwitch

KimiSwitch is a Tauri desktop app and local-only Kimi Code API-key pool for personal or authorized internal use.

Its desktop information architecture, visual tokens, and selected UI wrappers are adapted from [CC Switch](https://github.com/farion1231/cc-switch) under the MIT License to keep the workflow familiar. See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). KimiSwitch is not affiliated with Moonshot AI, Kimi, or the CC Switch project.

## What it does

- Runs a loopback proxy on `127.0.0.1:15721`.
- Keeps Kimi API keys in environment variables or macOS Keychain instead of command-line arguments.
- Supports manual active-key switching.
- Supports an optional automatic rotation mode:
  - `request`: recommended mode; least-requested-key routing, which keeps call frequency/order as even as possible.
  - `token`: optional best-effort balancing using observed provider usage plus an input-size reservation for in-flight requests.
- Retries a request on an upstream 401, 429, or 5xx response only when rotation is enabled and no successful stream has started.
- Puts rate-limited keys into a cooldown window.
- Provides per-account and test-all availability checks using a one-output-token completion, so authentication-only success cannot hide an exhausted quota.
- Classifies observed failures as exhausted quota, temporary rate limit, invalid key, disabled account, network failure, or upstream failure using HTTP status, provider error type, and message text.
- Quarantines failed accounts for at least 10 minutes. Healthy accounts are always preferred; a cooled account is retried only when no enabled healthy account remains.
- Records per-key and per-day request, success, error, input, output, cache-read, reasoning, observed-total, and estimated-token statistics.
- Provides a separate Kimi Code direct/KimiSwitch takeover switch. Takeover changes only model-provider routing, preserves the original provider, creates `~/.kimi-code/config.toml.kimiswitch.bak`, and can restore direct mode atomically.
- Does not scrape undocumented quota pages, spoof the client identity, or claim that locally observed usage is an account's remaining quota.

The token mode is deliberately described as best effort. Different accounts can have different plans, windows, limits, or request costs, so no local scheduler can guarantee identical remaining quota. It prevents the common case where sequential requests keep using one key while the others stay untouched.

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
go run ./cmd/kimi-switch rotation on request
go run ./cmd/kimi-switch serve
```

## Desktop installers

Build the macOS disk image locally:

```sh
pnpm tauri build --bundles dmg
```

The bundled app and installer show the original KimiSwitch icon and the attribution `Powered by kyuiazusa`. The icon uses a white sideways-pi switch mark with a small green status element; it is not a Kimi or CC Switch product mark.

The GitHub Actions workflow in `.github/workflows/build-installers.yml` builds a macOS DMG and a Windows NSIS setup EXE on version tags such as `v0.1.0`, or when started manually from the Actions page. Each installer is uploaded as a workflow artifact. The generated builds are unsigned until Apple and Windows signing credentials are configured.

For macOS Keychain storage:

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
export KIMI_MODEL_BASE_URL='http://127.0.0.1:15721/v1'
kimi
```

The local proxy consumes the client key and injects the selected upstream key. The Kimi Code CLI currently distinguishes API-key direct connections from OAuth-managed endpoints, and its model environment variables support a custom base URL; use the API-key path for this proxy.

## Import existing Kimi providers

KimiSwitch can extract `api_key` from `[providers.<name>]` or `KIMI_API_KEY` from `[providers.<name>.env]` in `~/.kimi-code/config.toml` and copy the values into macOS Keychain. It does not rewrite or delete the Kimi config:

```sh
go run ./cmd/kimi-switch init
go run ./cmd/kimi-switch import-kimi
```

OAuth-only providers are intentionally skipped. The importer is for API-key providers only.

## Kimi Code takeover switch

The desktop header has two independent switches:

- **Kimi Code direct / KimiSwitch takeover** controls whether models in `~/.kimi-code/config.toml` use their original provider or the local KimiSwitch provider.
- **Automatic rotation** controls how KimiSwitch chooses among imported accounts after takeover is enabled.

Import remains read-only. Enabling takeover keeps the original provider block intact, writes a marked `kimi-switch-local` provider, redirects only model `provider` references that used the active original provider, and creates a one-time backup. Disabling takeover restores those references and removes the managed block. After either change, run `/reload` in an already-running Kimi Code session; new sessions read the new configuration directly.

The usage page follows the familiar CC Switch breakdown while adapting it to Kimi's response fields: input, output, cache-read, reasoning, total, request outcomes, per-account totals, and 7/30-day trends. Real token values come from upstream `usage`; requests without a usable `usage` object are shown separately as estimates.

## Admin endpoints

All admin endpoints use the same local bearer key as the proxy.

```text
GET  /healthz
GET  /api/status
GET  /api/usage
PUT  /api/kimi/takeover          {"enabled":true}
POST /api/rotation              {"enabled":true,"mode":"token"}
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
