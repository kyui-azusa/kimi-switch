# Documentation Notes

Issue #2 delivers the manual first phase. Issue #13 tracks official synchronization as a separate second phase.

Phase 1 is manual-only:

- Record a reference time for the five-hour quota window.
- Record a reference time for the weekly quota window.
- Desktop and legacy web forms display and edit the reference time in the operator's local timezone.
- Configuration files and the status API store the same values in UTC RFC3339.
- Show the resulting next refresh time in the desktop UI.
- Label every manual value as `手动参考 · 非官方`.
- Do not use these values for rotation, account health, quota exhaustion judgement, or automatic switching.

Authoritative wording for later phases:

- `官方标准口径` only applies to a field that is directly returned by the official upstream, can be checked repeatedly with stable semantics, and has traceable provenance.
- Manual entry, test observation, derived estimate, `imported_at`, `Retry-After`, and provider error text are all `参考信息`, not the source of truth.
- Phase 1 only records manually entered reference data; no form value should be described as an official refresh commitment.

Detailed account-health language lives in [account-health.md](account-health.md).
