# Closed-PR watches and appeal review

Technical details for closed-PR observations, polling, and the Needs attention reader. Start with [the short evidence guide](evidence.md) for ordinary backlog download and comparison.

## Explicit closed-PR watches

Watches are local, repository-bound records for operator-selected closed PRs. They do not depend on the open inventory, ledger membership, a comparison, or a known automation actor. Capture an initial observation explicitly, then enroll its exact snapshot:

```sh
bin/cache fetch --kind pr --number 123 --profile closure-watch --mode refresh --request-budget 100
bin/cache watch-enroll --number 123 --snapshot SNAPSHOT_ID --by CONTRIBUTOR --closure-comment 456 --survivor 789
bin/cache watch-show --number 123
bin/cache watch-capture --number 123 --request-budget 100
```

Use the returned `snapshot_id`. Enrollment and `watch-show` are offline; fetch/capture and the explicit poll command below acquire evidence. The optional `--closure-comment` and `--closure-event` are positive source IDs; `--survivor` is another PR number in the same repository. `--closure-at` supplies a timezone-qualified timestamp. Without it, the initial summary's closure time is retained even if the PR later reopens. `--provenance` is an operator-supplied description, unverified as attribution; omission remains unknown. Source wording and actor names never establish automation provenance or authority.

The `closure-watch` profile captures summary/body, issue discussion and the [issue timeline](https://docs.github.com/en/rest/issues/timeline) using REST GETs. Capture shares one request budget across identity, pagination and final metadata checks and obeys persisted cooldowns. Stable timeline identities use event type plus numeric ID, node ID, or a committed event's SHA. Unsupported identity shapes produce explicit coverage gaps. Page objects and snapshots use the existing cache publication rules; full refresh may reuse no pages, but interrupted component evidence remains durable. This is initial observation capture, not a polling scheduler or incremental activity detector.

Watch records live in ignored `cache/watches/pr-N.json` beside the evidence they reference. Retain the entire cache to preserve watches and their evidence. Enrollment refuses an existing watch, an open initial PR, conflicting identities, unsupported versions or missing/corrupt selected evidence. A partial snapshot may be enrolled, but never claims a complete baseline. Capture preserves previous observations, including when the current PR is reopened or evidence acquisition is partial. Fatal failures before an observation exists return an error and retain the prior record. There is no live fallback from a failed offline read.

`watch-show` validates all retained snapshots and returns a versioned JSON view with source IDs, original payloads and timestamps, coverage gaps, the initial baseline's completeness and the last complete successful check. It includes all observed discussion/timeline entries, classifies their timestamp relative to the closure boundary, and exposes supplied reference matches or gaps. Unknown timestamps remain unknown. It never marks existing activity seen. A complete capture describes the endpoints observed at that time; it cannot establish deleted or inaccessible historical events or current GitHub state.

All retained observations are read in full; these commands currently bound requests, not total stored or output bytes. Use the ordinary pinned `cache chunk` reader for bounded component text. Discussion and commented timeline entries may represent the same source comment; components stay separate. Repeated captures preserve snapshot history. Explicit polling and the deduplicated readers are described below. Notifications opens the bounded attention reader and the separate imported-closure history reader.

## Explicit polling and activity history

`watch-poll` operates on one explicitly selected watched PR. Every new poll refreshes summary, discussion and timeline, sharing one request budget across identity reads, pages, overlap checks and the final metadata check:

```sh
bin/cache watch-poll --number 123 --request-budget 100
bin/cache watch-status --number 123
bin/cache watch-poll --number 123 --checkpoint WATCH_CHECKPOINT --request-budget 100
```

Use the current `checkpoint` from poll/status to continue an unfinished poll. Continuation preserves the poll ID and increments its attempt count. The token covers the complete watch record; concurrent or stale continuations fail before network access. An unfinished poll requires that token or explicit `--restart`. A completed poll starts a new acquisition when invoked without a token. `watch-capture` refuses to bypass an unfinished poll. No scheduler or background retry is started.

Resume revalidates all saved mutable discussion/timeline pages, including complete collections; terminal pages are read unconditionally to detect a new next page. Page reuse requires matching identity, revision, count, repository scope and a 900-second age window. Changed/expired pages are fetched again. Conditional requests still consume the budget. Very small budgets may repeatedly stop during revalidation; increase the explicit budget to make progress. This first implementation has no multi-watch runner, fairness scheduler or since-time shortcut that could miss older edits.

Each attempt records `running` before acquisition. After immutable evidence is published, one atomic watch update records its snapshot and `partial` or `complete` polling state. A recorded acquisition failure returns `status: error`, its diagnostic and consumed request count without adding a fabricated observation. These partial/error JSON results have exit status zero; invalid arguments, stale caller tokens, failed preflight validation and watch-publication failures exit nonzero. Errors encountered inside acquisition, including its local checkpoints, are recorded in the error result. Check the polling status and coverage rather than treating exit zero as success. Persisted cooldowns remain in force across attempts and restarts.

Interruption can leave a running marker and unreferenced cache snapshots. Inspect `watch-status` for the committed token. If a continuation's selected acquisition snapshot has been replaced by another capture or an interrupted acquisition, it records an error without issuing a request; explicitly `--restart` to begin fresh. An initial attempt interrupted before recording any selected snapshot can safely start its acquisition again. Restart retains all prior committed observations. Poll completion means the requested source components were complete at observation time; closure-context gaps, old baseline gaps and current GitHub freshness remain separate.

### Offline history, references and source fragments

```sh
bin/cache watch-history --number 123 --limit 20
bin/cache watch-history --number 123 --offset 20 --limit 20 --checkpoint HISTORY_CHECKPOINT
bin/cache watch-history --number 123 --entry ENTRY_ID --limit 20
bin/cache watch-source --number 123 --entry ENTRY_ID --reference 0 --checkpoint ENTRY_CHECKPOINT --max-bytes 4096
```

History uses versioned `watch-source-revisions-v1` identities. Repeated captures of the same source/content revision produce one entry with a reference count and first/last references. Entries follow first-observed order, with supplied source timestamps retained; this does not infer the chronology of missing events. Older comment revisions, reopen/close events, enrollment activity and uncertain provenance remain inspectable. Absence from a later observation never means deletion, withdrawal or acknowledgment.

Numeric comment IDs shared by discussion and commented timeline entries use one explicit revision projection: body, created/updated times, node ID and author ID/node ID/login. Matching projections share an entry; differing projections remain distinct revisions. Other raw differences, including endpoint metadata and reactions, remain accessible in the original references and do not generate content activity on their own. Other timeline events retain their stable event identity and full raw-content digest. No account or wording filter suppresses reviewable activity.

Use `--entry` at offset zero to get that entry's reference-page checkpoint. It is distinct from the top-level history token. Each reference pins a snapshot, component, exact source-row offset and raw-content digest. Subsequent reference pages require their entry-bound token. `watch-source` requires the entry token and confines byte continuation to the selected reference; it never advances into the next comment. Concatenate UTF-8 fragments before parsing the original JSON row. Changing the watch invalidates these continuation tokens; restart discovery to inspect the new history.

History/reference pages default to 20 records and cap at 100; source fragments default to 4 KiB and cap at 64 KiB. Metadata strings, total input bytes, memory and verification time are not capped: every call verifies/scans retained snapshots. Coverage summaries accompany history; `watch-status` includes up to 20 current gaps plus an omission count, and `watch-show` retains the full observation details. None of these offline readers acquires evidence, changes read/unread state, or grants approval. Full snapshots remain the durable history; the deduplicated view is derived, with no mutable index to adopt as evidence.

## Needs attention: bounded offline readers

Choose **Notifications** in the TUI sidebar; unviewed watched PR activity and imported actions appear in **Needs attention**, while viewed records appear in **Past actions**. The attention reader covers all retained PR watches, including PRs absent from the ledger. It does not fetch or create a watch. Unavailable/corrupt watches remain visible but unselectable until dismissed. Watch polling errors, incomplete/running polls and source gaps remain distinct from a human-confirmed appeal. Closed issues are not currently watched by the saved PR watch reader; issues tracked with `w` do appear as comment notifications.

`Tab` selects a row; `Enter` opens its PR group. The second and final screen shows chronological saved comment excerpts or attributed closure explanations. Previous/More cards page within that screen; `Esc` or `h` returns. The TUI does not open raw references or watch context as further screens. History retains pre-closure discussion, closure rationale references, edited comments and conflicting observations. It sorts by the earliest valid supplied source timestamp, then first observation and stable ID; unknown dates sort last. This does not prove complete historical chronology or current GitHub state.

Raw references, full source text and coverage remain available through the offline CLI, including cross-endpoint observations of one revision. TUI excerpts and omissions are bounded. Repository/install changes and newer reads reject late replies. Opening this screen preserves unsaved decisions. Reading never acknowledges anything: acting on a listed closure happens outside this tool.

The same readers are available as JSON CLI commands:

```sh
bin/cache attention-list --limit 10
bin/cache attention-list --offset 10 --limit 10 --checkpoint LIST_CHECKPOINT
bin/cache attention-read --number 123 --section context --checkpoint WATCH_CHECKPOINT
bin/cache attention-read --number 123 --section history --checkpoint WATCH_CHECKPOINT
bin/cache attention-read --number 123 --section references --entry SOURCE_REVISION_ID --checkpoint WATCH_CHECKPOINT
bin/cache attention-source --number 123 --section history --entry SOURCE_REVISION_ID --reference 0 --checkpoint WATCH_CHECKPOINT --max-bytes 4096
```

Use the list's top-level checkpoint for list continuation and its selected row's `watch_checkpoint` for every detail/source request. List tokens bind repository, reader policy, watch membership and every watch file's content; any watch publication invalidates list continuation. Detail/source requests bind the complete selected watch checksum plus explicit section, entry, raw reference and offsets. Pass the same selection when following a continuation. Detail reads recheck the watch after evidence verification; changes require restarting. These tokens do not bind cache availability or grant authority. Source corruption is checked on every read and never triggers live fallback.

All responses use schema 1 and `attention-reader-v1`, include repository and zero `requests`, and put diagnostics on stderr with nonzero exit status for stale/invalid requests. A list can succeed with unavailable rows; `selectable: false` makes their failure explicit. Lists and detail pages default to 10 rows and cap at 50; the TUI requests five. Each preview caps at 1,200 UTF-8 bytes and each label at 240, with omitted byte counts. Source fragments default to 4 KiB and cap at 16 KiB, reporting exact byte omissions and the next offset. Source continuation never crosses its selected row. Concatenate fragments before parsing JSON; UTF-8 boundary errors fail explicitly. Large context and attributed reasons are available through the same source reader rather than silently clipped.

Only output is bounded: catalog hashing and selected-watch evidence verification can scan full files/history and use unbounded input memory. There is no background monitoring, freshness guarantee, confirmed-appeal classification or execution authority.
