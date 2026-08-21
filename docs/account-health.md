# Account Health And Quota Refresh

Issue #2 adds two optional manual reference records to each account:

- `five_hour`
- `weekly`

Each persisted record stores:

- `anchor_at`
- `recorded_at`
- `source`
- `authoritative`

`next_refresh_at` is computed for the status API from the persisted anchor; it is not written back to the configuration.

Desktop and legacy web forms edit `anchor_at` in local time. Configuration files and API payloads store `anchor_at`, `recorded_at`, and `next_refresh_at` as UTC RFC3339 timestamps.

Manual API writes use the same nested shape as the status response:

```json
{
  "quota_refresh": {
    "five_hour": { "anchor_at": "2026-08-21T09:00:00Z" },
    "weekly": { "anchor_at": "2026-08-28T00:00:00Z" }
  }
}
```

Omitting `quota_refresh` preserves both records. Omitting one child preserves that record, while an empty `anchor_at` clears it. The backend temporarily accepts the earlier flat field names for compatibility, but new clients should use the nested shape above.

Phase-1 policy:

- Values are entered manually.
- Values are for test planning and observation only.
- The UI must present them as non-authoritative reference metadata.
- These records do not participate in account rotation.
- These records do not decide whether an account is healthy.
- These records do not decide whether a quota is exhausted.

Authoritative-source wording:

`官方标准口径` only means a refresh field that the official upstream exposes directly and that KimiSwitch can sync with stable, reviewable provenance. Anything inferred from manual input, local observations, import timing, `Retry-After`, or error responses remains reference-only.
