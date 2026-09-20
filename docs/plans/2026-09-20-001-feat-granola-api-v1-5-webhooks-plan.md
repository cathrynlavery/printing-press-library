---
title: "feat(granola): catch up to API v1.5 and add webhook operations"
type: feat
status: implemented
date: 2026-09-20
target_repo: github.com/mvanhorn/printing-press-library
target_path: library/productivity/granola
review_page: docs/reviews/2026-09-20-granola-cli-api-v1-5-review.html
---

# feat(granola): catch up to API v1.5 and add webhook operations

## Summary

Patch the published `granola-pp-cli` in place to match Granola's current public API while preserving its 34 catalog-side customizations. The work adds webhook endpoint management and Standard Webhooks signature verification, closes the existing v1.5 read gaps (paginated transcripts, folder-filtered notes, private notes, speaker attribution, and the Enterprise audit feed), and fixes a reproducible concurrent SQLite-open failure found during the audit.

This is deliberately not a broad reprint. Granola's generated base has accumulated authentication, encrypted-cache, dual-source sync, merge, staleness, and transcript-retention invariants that a fresh print could silently erase. The published artifact should be fixed first, each durable customization recorded in `.printing-press-patches/`, and generator-shaped improvements carried upstream separately where a local generator checkout is available.

## Recommendation

Ship the work as one combined scoped catalog patch from this repo, matching the user's decisions. The patch fixes concurrent store initialization, adds v1.5 read parity, and delivers webhook create/list/update/delete plus offline signature verification without reprinting the curated CLI.

Do not add a local webhook receiver or tunnel in this round. Granola requires a publicly reachable HTTPS endpoint, and a CLI-owned receiver would introduce long-running process, TLS, exposure, and delivery-state responsibilities that are not needed to manage endpoints or verify deliveries.

## Initial Evidence Snapshot

Audit performed against the live official docs and OpenAPI on 2026-09-20:

- Live OpenAPI: `9` operations across `7` paths.
- Archived `library/productivity/granola/spec.json`: `3` operations across `3` paths.
- Missing operations: `GET /v1/notes/{note_id}/transcript`, `GET /v1/audit`, and create/list/update/delete for `/v1/webhook-endpoints`.
- Missing query support: `folder_id` on `GET /v1/notes`.
- Missing response fields: `private_notes_text`, `private_notes_markdown`, and `speaker.attribution`.
- Existing `transcript get` uses the local store or an unofficial internal API, so it does not provide the official cursor-paginated fallback required when Get Note returns `413 TRANSCRIPT_TOO_LARGE`.
- Existing API hydration maps `summary_markdown` / `summary_text` into `meetings.notes_markdown` / `notes_plain`. Granola v1.5 now exposes the actual owner-written private notes, so the current mapping mislabels AI summary text as human notes.
- `tools-manifest.json` still contains only the original three public operations.
- `go vet ./...` and both CLI/MCP builds pass.
- `go test ./...` fails at `TestMigrate_ConcurrentFreshDB`; the focused test fails reproducibly with `acquiring migration connection: database is locked (5) (SQLITE_BUSY)`.

Primary sources:

- [Granola API introduction](https://docs.granola.ai/introduction)
- [Live OpenAPI](https://docs.granola.ai/api-reference/openapi.json)
- [API changelog](https://docs.granola.ai/api-reference/changelog)
- [Webhooks](https://docs.granola.ai/webhooks)
- [Audit API](https://docs.granola.ai/audit)

## Goals

1. Cover every operation in the current official OpenAPI without regressing the CLI's curated local/cache capabilities.
2. Make large transcripts reliable by paging the dedicated endpoint after a `413` (and when explicitly requested).
3. Keep human-authored notes, AI summaries, and transcript speaker identity semantically distinct in the local store.
4. Make webhook mutations safe for humans and agents: typed inputs, dry-run, explicit confirmation for deletion, non-read-only MCP annotations, and no implicit secret persistence.
5. Verify Granola webhook signatures locally using the Standard Webhooks contract before parsing JSON.
6. Support the Enterprise audit feed with its own credential and serial pagination semantics.
7. Restore a green, repeatable Go test baseline.

## Non-goals

- Hosting a webhook receiver, opening a tunnel, provisioning TLS, or replaying deliveries.
- Persisting webhook signing secrets in the CLI config or local SQLite database.
- Inventing a webhook test-delivery command; Granola's public API does not document one.
- Changing the per-CLI release ledger, changelog, or runtime version manually; post-merge automation owns those.
- Replacing the 34 existing Granola patch records or broadly regenerating the tree.
- Changing Granola's unofficial device-grant/internal-API integrations except where tests are needed to prove they remain intact.

## User-facing Command Design

```text
granola-pp-cli notes list --folder-id fol_... --all

granola-pp-cli transcript get not_... --data-source live

# transcript get follows all official transcript pages automatically

granola-pp-cli audit list --action workspace --occurred-after 2026-09-01 --all

granola-pp-cli webhooks list
granola-pp-cli webhooks create \
  --url https://example.com/granola-webhooks \
  --scope personal --scope public \
  --event note.generated --event note.edited \
  --folder-id fol_...
granola-pp-cli webhooks update whe_... --enabled=false
granola-pp-cli webhooks delete whe_...

GRANOLA_WEBHOOK_SECRET=whsec_... \
  granola-pp-cli webhooks verify \
  --body-file payload.json \
  --webhook-id "$WEBHOOK_ID" \
  --webhook-timestamp "$WEBHOOK_TIMESTAMP" \
  --webhook-signature "$WEBHOOK_SIGNATURE"
```

`webhooks verify` also accepts the raw body on stdin. It verifies the signature and timestamp before JSON decoding, emits the typed event only after verification, and never prints the secret.

## Architecture

```text
Official API key (GRANOLA_API_KEY)
  ├─ notes / folders
  ├─ paginated transcript
  └─ webhook endpoint CRUD

Audit key (GRANOLA_AUDIT_API_KEY)
  └─ serial, cursor-paginated audit feed

CLI-owned Granola session / local cache
  └─ existing curated commands and local SQLite store

Webhook signing secret (GRANOLA_WEBHOOK_SECRET)
  └─ offline verifier only; never persisted
```

The official transcript helper is shared by the friendly `transcript get` command and public-API hydration. This avoids creating two subtly different pagination implementations. The current local/store/internal behavior remains the fallback for legacy UUID-backed meetings and offline reads.

## Implementation Units

### U0. Preflight and preservation contract

**Goal:** Prevent a broad spec refresh from erasing Granola-specific behavior.

**Work:**

- Create a clean branch from current `main` after preserving unrelated work in the current checkout.
- If an issue is used, check assignees/comments and post the mandatory claim before code changes.
- Snapshot `agent-context --json`, the 34 patch IDs, and the existing top-level command names into a focused regression fixture/test.
- Do not modify `.printing-press-release.json`, `CHANGELOG.md`, runtime version variables, `registry.json`, or `cli-skills/pp-granola/SKILL.md`.

**Exit:** A test fails if a later step drops an existing curated command or patch-backed invariant.

### U1. Fix concurrent fresh-database initialization

**Goal:** Make the current test suite green before layering API work on it.

**Files:**

- `internal/store/store.go`
- `internal/store/schema_version_test.go`
- `.printing-press-patches/concurrent-store-open.json`

**Approach:**

- Move migration-connection acquisition behind the same bounded `SQLITE_BUSY` retry policy used by the migration lock, or remove connection-time `journal_mode(WAL)` contention and set WAL after acquiring the serialized migration lock.
- Preserve context cancellation and the existing hard migration deadline.
- Close failed/partial connections and avoid unbounded retry.

**Tests:**

- Run `TestMigrate_ConcurrentFreshDB` repeatedly and under `-race`.
- Retain cancellation, newer-schema refusal, and migration-lock tests.
- Run the full module suite before starting U2.

### U2. Classify the official API contract drift

**Goal:** Record and implement live v1.5 drift without pretending the original generated contract was freshly archived.

**Files:**

- `tools-manifest.json`
- `.printing-press-patches/granola-api-v1-5-and-webhooks.json`

**Approach:**

- Compare the official OpenAPI at `https://docs.granola.ai/api-reference/openapi.json` with the archived generation artifact.
- Preserve `spec.json` and its checksum as original-run provenance; implement drift in the curated runtime layer.
- Preserve unknown audit `action` strings and unknown `data` fields; the official contract says both can expand.
- Treat nullable fields as nullable. Do not collapse `null` into a misleading empty value where ownership/access matters.

**Tests:**

- Add a contract test asserting the expected path/method set.
- Add fixture decoding for workspace webhook endpoints (`created_by: null`, `scope: workspace`) and v1.5 note/speaker fields.

### U3. Complete note and transcript parity

**Goal:** Make current official note data usable through both direct commands and sync.

**Files:**

- `internal/cli/notes_list.go`
- `internal/cli/transcript.go`
- `internal/cli/sync_api_hydrate.go`
- `internal/granola/api_notes.go`
- `internal/granola/api_notes_test.go`
- `internal/granola/store_sync.go`
- `internal/granola/store_sync_test.go`
- `internal/cli/sync_api_hydrate_test.go`

**Approach:**

- Add `--folder-id` to `notes list` and pass it through pagination.
- Add a shared official transcript pager (page size `1..100`, cursor loop, `hasMore` termination, repeated-cursor guard, context checks).
- When `Get Note?include=transcript` returns `413 TRANSCRIPT_TOO_LARGE`, fetch note detail without inline transcript and hydrate the transcript through the pager.
- Teach `transcript get --data-source live` to prefer the official API for `not_...` IDs when an API key is configured; keep store/cache/internal fallback behavior for offline and legacy IDs.
- Preserve `speaker.attribution`, `speaker.name`, and `diarization_label`.

**Tests:**

- 413 fallback, multiple pages, empty final page with `hasMore`, repeated cursor, 401/403, context cancellation, and `--all` behavior.
- macOS speaker source, iOS all-microphone diarization, and attribution-present fixtures.
- Ensure transcript retention still prevents a smaller upstream transcript from replacing a larger stored copy.

### U4. Separate private notes from AI summaries

**Goal:** Stop presenting API summaries as user-written notes and take advantage of the new v1.5 private-note fields.

**Files:**

- `internal/granola/api_notes.go`
- `internal/granola/store_sync.go`
- `internal/cli/granola_helpers.go`
- `internal/cli/export.go`
- `internal/cli/show.go`
- relevant store/read/export tests
- `.printing-press-patches/private-notes-summary-separation.json`

**Approach:**

- Add nullable `summary_markdown` and `summary_plain` columns to the domain `meetings` table and bump the store schema version.
- Map `private_notes_markdown` / `private_notes_text` to `notes_markdown` / `notes_plain` only when present.
- Map API summaries to the new summary columns and use them as the offline AI-summary fallback when no live/cached panel is available.
- Never clear cache-derived human notes because a shared note returns `private_notes_*: null`.
- For existing API-owned rows only, migrate the known old summary-as-notes value into summary columns. Do not guess on mixed/cache-owned rows; a subsequent owner-key sync can fill the correct private notes safely.

**Tests:**

- Owner note, shared note, workspace-key note, mixed cache/API row, upgrade from schema v4, and no-data-loss downgrade refusal.
- `notes-show` renders only human notes; `show`/`export` render human notes and summary in separate sections.

### U5. Use attribution in talk-time calculations

**Goal:** Make talk-time meaningful for iOS transcripts where every item currently reports `speaker.source=microphone`.

**Files:**

- `internal/granola/store_sync.go`
- `internal/cli/talktime.go`
- transcript/talk-time tests

**Approach:**

- Add a nullable `attribution` column to `transcript_segments` in the same schema migration as U4.
- Prefer `attribution=me|them`; fall back to the existing microphone/system heuristic when attribution is absent.
- Keep anonymous diarization labels distinct rather than presenting them as identified people.

### U6. Add the Enterprise audit feed

**Goal:** Expose `GET /v1/audit` without breaking normal API-key sync.

**Files:**

- `internal/cli/audit.go` (new)
- `internal/cli/root.go`
- `internal/config/config.go` or a narrow audit-client constructor
- audit command tests
- README/SKILL auth documentation

**Approach:**

- Require `GRANOLA_AUDIT_API_KEY`; never silently substitute the regular API key.
- Add `audit` to the auto-refresh skip list so an audit command does not run unrelated note/cache sync first.
- Support `--action`, `--occurred-before`, `--occurred-after`, `--cursor`, `--page-size`, and `--all`.
- Page serially, never in parallel; preserve `collected_at` ordering and document the one-year retention window.
- Keep actor variants and `data` open-ended.

**Tests:** pagination, short page with `hasMore=true`, open action strings, user/system/anonymous actors, one-year validation error, audit-only credential selection, and no auto-refresh.

### U7. Add webhook endpoint management

**Goal:** Cover every documented webhook management operation safely.

**Files:**

- `internal/cli/webhooks.go` (new)
- `internal/cli/root.go`
- webhook command/client tests
- `.printing-press-patches/webhook-management.json`

**Command contracts:**

- `webhooks list` — read-only, no pagination in the current API.
- `webhooks create` — required `--url` and at least one `--scope`; repeatable scope/event/folder flags; omitted events means all events.
- `webhooks update <id>` — partial PATCH; fail locally when no field changes; allow `--folder-id` replacement and `--clear-folders`; allow `--enabled=true|false` with explicit changed-state detection.
- `webhooks delete <id>` — confirm unless `--yes`; support `--dry-run`; mark as a mutation in MCP annotations.

**Safety and validation:**

- Validate `https` and non-empty host locally, but let Granola decide public reachability.
- Validate `whe_...` / `fol_...` shapes, supported scopes, and supported events before sending.
- Respect workspace-key scope rules (`["workspace"]` exactly).
- Add `webhooks` to the auto-refresh skip list.
- Never cache webhook list results across mutations.
- Creation's `signing_secret` must survive default and `--agent` output even though agent mode enables `--compact`; it is returned once. Do not save it automatically, print it to stderr, include it in dry-run output, or add it to config/state.
- List/update responses must honor `url_redacted` and nullable `created_by`.

**Tests:** complete request bodies, omitted events, clear folders, enabled false, empty update rejection, confirmation/no-input behavior, dry-run, 403 scope errors, 404 plan availability, redacted URL, workspace-managed endpoint, cache invalidation, and secret-preserving agent output.

### U8. Add offline Standard Webhooks verification

**Goal:** Let users validate deliveries without operating a server inside the CLI.

**Files:**

- `internal/granola/webhookverify.go` (new)
- `internal/granola/webhookverify_test.go` (new)
- `internal/cli/webhooks.go`
- `.printing-press-patches/webhook-signature-verification.json`

**Approach:**

- Read `whsec_...` from `GRANOLA_WEBHOOK_SECRET`; do not accept it as a positional argument.
- Build `{webhook-id}.{webhook-timestamp}.{raw-body}` exactly.
- Base64-decode the portion after `whsec_`, compute HMAC-SHA256, accept any valid `v1,<base64>` signature in the space-separated header, and compare in constant time.
- Enforce the documented five-minute replay tolerance with a test-injected clock in the pure verifier.
- Read raw bytes from `--body-file` or stdin. Verify before JSON decode.
- Validate `webhook-id == event_id` after signature verification and preserve unknown future event types/data.

**Tests:** valid signature, wrong body, wrong secret, malformed/unknown signature versions, multiple signatures during rotation, stale/future timestamp, whitespace/body-byte fidelity, ID mismatch, and secret non-disclosure in errors.

### U9. Align MCP, discovery, and documentation

**Goal:** Make every new capability discoverable without overstating coverage or hiding mutations.

**Files:**

- `README.md`
- `SKILL.md` (source only; do not edit the generated `cli-skills` mirror)
- `internal/cli/which.go`
- `internal/cli/agent_context.go` tests/fixtures as needed
- `tools-manifest.json`
- MCP walker/tool tests

**Documentation corrections:**

- Replace the absolute “Every Granola feature” claim with a dated, testable public-API coverage statement.
- Make `auth login` + `sync` the primary current-install quick start; present `sync-api` as the Business/Enterprise API-key path. The current README contradicts itself here.
- Explain personal/public/workspace keys, the separate audit key, and Business/Enterprise webhook availability.
- Correct “When Not to Use” language: the CLI already exposes meeting delete/restore and will expose webhook mutations. Require dry-run/help/confirmation instead of claiming the CLI is read-only.
- Document transcript `413` fallback, private-note ownership rules, webhook retries/deduplication, disabled-endpoint event loss, and one-time signing-secret handling.
- Mark webhook mutations non-read-only in MCP and verify the dynamic Cobra mirror exposes them with the right annotations.

### U10. Verification and live smoke test

**Automated:**

```bash
go test ./...
go test -race ./internal/store ./internal/granola ./internal/cli
go vet ./...
go build ./cmd/granola-pp-cli ./cmd/granola-pp-mcp
python3 .github/scripts/verify-skill/verify.py library/productivity/granola
python3 .github/scripts/verify-supply-chain/scan.py --base-ref origin/main
```

Also run the repo's publish-package/convention verifier applicable to an existing CLI patch and confirm no generated registry/skill-mirror/release-ledger files entered the diff.

**Fixture-backed CLI smoke:**

- Every new command's `--help`, JSON, compact, selection, dry-run, and typed error behavior.
- MCP tool enumeration and read-only annotations.
- Re-run the 34-patch preservation contract from U0.

**Live (credential names/metadata only; never print values):**

- With a personal/workspace API key: list notes by folder and retrieve a transcript large enough to exercise paging if available.
- With an audit key: fetch one page, then a serial multi-page window.
- With explicit user approval because it changes remote state: create a temporary webhook endpoint, list it, pause/resume it, send a locally constructed signed fixture through `webhooks verify`, and delete the endpoint.
- If live credentials or a safe public endpoint are unavailable, report those steps as **unverified** rather than inferring success from fixtures.

## PR and Patch Strategy

### One combined PR — `feat(granola): catch up to API v1.5 and add webhooks`

Units U0–U10 ship together so the schema migration, direct API commands, webhook tooling, MCP surface, and documentation stay atomic. One combined `.printing-press-patches/granola-api-v1-5-and-webhooks.json` records the catalog customization.

For the PR:

- Add only the matching `.printing-press-patches/*.json` entries.
- Preserve the permanent creator header; Cathryn Lavery is already listed as a contributor, so do not add duplicate attribution.
- Let the post-merge release-ledger workflow write the CalVer/changelog/runtime stamps.
- Read the latest Greptile summary and run `.github/scripts/pr-review-state/greptile_feedback.py <PR>` before calling it ready.
- Do not merge without explicit sign-off.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Reprint erases curated auth/cache behavior | Patch published CLI in place; preservation contract; upstream generator work separately |
| One-time webhook secret lost under `--agent --compact` | Command-specific output test requiring `signing_secret` in the default agent envelope |
| Signing secret leaks into config/logs/history | Environment-only verifier secret; no persistence; secret-safe errors; no stderr echo |
| 413 drops an entire sync item | Retry detail without transcript, then page transcript separately |
| Private notes overwrite cache data with null | Nullable fields + merge-only-on-present semantics |
| Old summary-as-notes rows remain misleading | Safe migration for API-owned rows; no destructive guess on mixed rows; rehydrate |
| iOS talk-time reports everyone as “me” | Store and prefer `speaker.attribution`; fallback only when absent |
| Audit pagination skips events | Page on `hasMore`/cursor serially, not result count; preserve `collected_at` order |
| Webhook endpoint URL contains credentials | Honor `url_redacted`; never attempt to reconstruct the path |
| Concurrent first run fails with SQLite busy | Serialize/retry connection acquisition under bounded context; repeat/race tests |

## Acceptance Criteria

- [x] Full Go test suite passes repeatedly; concurrent fresh-store test is stable under `-race`.
- [x] CLI covers all nine operations in the live official OpenAPI.
- [x] `notes list --folder-id` works and validates server errors cleanly.
- [x] Large transcripts hydrate and render through the paginated official endpoint after `413`.
- [x] Human private notes and AI summaries remain separate through sync, store, show, and export.
- [x] Talk-time prefers official `me`/`them` attribution when available.
- [x] Audit commands use the audit credential and never auto-refresh notes first.
- [x] Webhook create/list/update/delete work with correct mutation annotations, dry-run, and confirmation behavior.
- [x] Webhook creation retains the one-time signing secret in default agent output without persisting it.
- [x] Offline verification rejects tampered/replayed deliveries and never exposes the secret.
- [x] README/SKILL/agent-context/MCP surfaces agree with runtime behavior.
- [x] All existing Granola patch records and curated commands remain present.
- [x] No manual release-ledger, registry, or generated skill-mirror edits are included.
- [x] Live credential and remote-mutation checks are handed off as **unverified**; no safe keys or public receiver were placed in scope.

## Decision Record

Confirmed by Cathryn Lavery in chat on 2026-09-20:

1. **Delivery path:** scoped catalog patch.
2. **Webhook runtime:** management plus offline verifier; no local listener.
3. **PR shape:** one combined PR.

Implementation note: the archived `spec.json` remains the immutable contract artifact from the original Printing Press run, so this hand patch does not rewrite its checksum or claim fresh generation. Current runtime coverage is implemented in the curated command/client layer and recorded in the combined patch metadata. `transcript get` always follows all official pages rather than exposing partial-page flags.
