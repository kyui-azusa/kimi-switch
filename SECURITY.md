# Security policy

KimiSwitch holds API credentials and installs its own updates, so both the key handling and the
update chain are part of its security surface. This document describes what the project protects,
what it does not, and how to report a problem.

## Reporting a vulnerability

Please report security issues privately through
[GitHub's private vulnerability reporting](https://github.com/kyui-azusa/kimi-switch/security/advisories/new)
rather than opening a public issue.

Include the KimiSwitch version, your operating system, and the steps needed to reproduce the
problem. Expect an initial response within seven days. Please do not include real API keys in a
report — a redacted prefix such as `sk-abc…` is enough.

## Supported versions

Only the latest release receives security fixes. Fixes ship as a new patch release through the
normal signed update channel; there are no backports to older versions.

| Version | Supported |
| --- | --- |
| Latest release | Yes |
| Everything older | No — update first |

## What KimiSwitch protects

**Credential storage.** API keys are read from environment variables or the OS credential store
(macOS Keychain, Windows Credential Manager). They are never passed as command-line arguments,
where other local processes could read them from the process list.

**Request data.** The proxy holds request and response bodies in memory only while forwarding
them. Persisted state contains account metadata and usage counters — not prompts or completions.

**Local network exposure.** The proxy binds `127.0.0.1:15722` by default, so it is not reachable
from other machines. Every proxy and admin endpoint requires the local bearer key
(`local_api_key`).

**Update integrity.** Updates are verified with a minisign signature checked inside the app before
installation. The signing private key lives only in CI secrets and an owner-only local backup, and
is never present in this repository. Because verification happens client-side, an attacker who
compromised an update server still could not make KimiSwitch install a modified build — they could
at most deny updates. The public key is committed in `src-tauri/tauri.conf.json`, as intended.

**Config safety.** Enabling takeover rewrites only model-provider routing in
`~/.kimi-code/config.toml`, preserves the original provider block, and writes a one-time
`config.toml.kimiswitch.bak` backup before the first change.

## What KimiSwitch does not protect against

- **A compromised local account.** Any process running as your user can read the same credential
  store entries and reach the loopback proxy. KimiSwitch is not a sandbox.
- **Changing `listen_addr` to a non-loopback address.** If you bind the proxy to `0.0.0.0` or a LAN
  address, anyone who can reach that port and knows the local bearer key can spend your upstream
  quota. If you do this deliberately, change `local_api_key` from its default `local-kimi-switch`
  to a long random value first.
- **Upstream provider behavior.** Account limits, suspensions, and terms of service are between you
  and the provider. KimiSwitch rotates among accounts you already control; it does not create
  quota, scrape undocumented endpoints, or spoof client identity.
- **Unofficial builds.** Only releases published from
  [this repository's releases page](https://github.com/kyui-azusa/kimi-switch/releases) are signed
  with the project's updater key. Builds redistributed elsewhere are not covered by this policy.

## Hardening checklist

- Keep `listen_addr` on `127.0.0.1`.
- Replace the default `local_api_key` if anything other than your own tools talks to the proxy.
- Prefer the OS credential store over environment variables for long-lived keys.
- Install updates from within the app or from the official releases page.
