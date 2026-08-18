# Application updates

KimiSwitch uses the Tauri 2 updater. The app checks the HTTPS manifest in the background after startup and also exposes a manual check in Settings. A downloaded artifact is installed only after its updater signature is verified.

## First updater release

Version `0.2.0` is the first release that contains the updater plugin. Versions `0.1.x` cannot update themselves, so users must download and install `0.2.0` manually once. Installing over an existing copy does not replace KimiSwitch configuration, local statistics, Keychain or Credential Manager entries, or `~/.kimi-code/config.toml`.

## Signing keys

The public updater key is committed in `src-tauri/tauri.conf.json`. The matching private key is stored as the GitHub Actions secret `TAURI_SIGNING_PRIVATE_KEY` and in the local owner-only backup `~/.config/kimi-switch/signing/updater.key`. The backup directory is mode `700` and the key is mode `600`; the key must never be committed, copied into a public release directory, or placed in a web-served directory.

Updater signing verifies the downloaded update archive. It does not replace platform code signing:

- Production macOS releases still need an Apple Developer ID certificate, hardened runtime, and notarization credentials.
- Production Windows releases still need an Authenticode certificate and timestamp service.
- Those credentials should be separate CI secrets and should not reuse the Tauri updater key.

If the updater private key is lost, existing installations cannot trust artifacts signed by a replacement key. Rotate it only by shipping an app version that contains the new public key through the existing trusted channel first.

## Release workflow

1. Keep the versions in `package.json`, `src-tauri/Cargo.toml`, and `src-tauri/tauri.conf.json` identical.
2. Push a matching tag such as `v0.2.0`.
3. GitHub Actions builds macOS Apple Silicon, macOS Intel, and Windows x86_64 artifacts with `TAURI_SIGNING_PRIVATE_KEY`.
4. The publish job refuses to create `latest.json` unless all three updater archives and signatures are present.
5. The workflow publishes versioned files first and `latest.json` last, so a partial build cannot replace the previous usable manifest.

The checked-in endpoint uses GitHub Releases:

```text
https://github.com/kyui-azusa/kimi-switch/releases/latest/download/latest.json
```

## Optional static server

The same workflow can mirror release files to an HTTPS static directory. Configure these repository secrets:

| Secret | Purpose |
| --- | --- |
| `UPDATE_BASE_URL` | Public version directory, for example `https://updates.example.com/kimi-switch/releases/v0.2.0` |
| `UPDATE_DEPLOY_HOST` | SSH host used by the publish job |
| `UPDATE_DEPLOY_USER` | Restricted deployment account |
| `UPDATE_DEPLOY_PATH` | Server directory that contains `latest.json` and `releases/` |
| `UPDATE_DEPLOY_SSH_KEY` | Private deployment key |
| `UPDATE_DEPLOY_KNOWN_HOSTS` | Pinned SSH host-key line for the deployment server |

Serve `latest.json` as `application/json` with `Cache-Control: no-cache, max-age=0`. Versioned archives and `.sig` files should use long-lived immutable caching. Keep directory listing disabled and allow only HTTPS. Update the endpoint in `tauri.conf.json` when the static server becomes the primary source.

## Rollback and recovery

Do not delete the last known-good release. If publishing fails before `latest.json`, no rollback is needed because the previous manifest remains active. If a bad release is published, stop serving its manifest and restore the previous `latest.json` for clients that have not updated. Clients already on the bad version do not downgrade automatically; publish a fixed version with a higher semantic version, or provide a manual signed installer for recovery.

Before promoting a release, install the previous updater-enabled version in a clean macOS and Windows environment and verify detection, progress reporting, signature validation, installation, relaunch, and persistence of accounts, credentials, proxy settings, rotation settings, and statistics.
