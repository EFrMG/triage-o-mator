# Local storage and recovery

Transactional data such as the ledger, groups, cache metadata, watches and imported closure histories is written by staging a temporary file, flushing it, replacing the destination, then flushing the directory entry. Readers see the previous complete file or the new one, never a partly rewritten one. Reports and CSV exports are not covered by this guarantee. This depends on a filesystem that supports POSIX locks, atomic rename, and `fsync`.

Locks coordinate cooperating processes sharing one filesystem, not separate clones, Git merges or GitHub writes. The ledger writers (`apply`, `import-csv`, `sync`, and `item-score`) serialize their complete read-modify-write transactions under one ledger lock. This preserves unrelated concurrent updates within an install. CSV import also checks each exported row revision against the current ledger and rejects the entire import if any row changed; export again before retrying. The revision includes the configured `owner/repo` name and all original ledger fields except `last_synced_at`, so routine sync timestamps do not invalidate a spreadsheet. Ordinary `apply` does not use a row revision.

## Interrupted writes and fetches

Locks and temporary files live in a `local/` directory beside the data being written. It creates its own `.gitignore` containing `*`, so these files are ignored even in an existing install without updated installer rules. That directory must be on the same filesystem as its destination. Do not remove or replace lock files while any writer may be running: the stable lock file is what makes waiting writers cooperate.

An interruption before replacement leaves the old complete file; an interruption after replacement can leave the new complete file. A reported filesystem error after replacement may therefore mean the change is already visible: inspect the file before retrying. A killed process can leave ignored temporary files, but they are never read as data. The OS releases its lock when the process exits. Unused temporary files can be cleaned up when all writers are stopped; keeping them does not affect recovery.

`fetch` and `sync` share an inventory lock, which acquisition holds while reading GitHub. Inventory metadata includes a checksum of the raw JSONL. Metadata is published first; a crash between its publication and the raw file's publication leaves a checksum mismatch, which `sync` refuses. Recover with `bin/fetch --full`, then `bin/sync`. Legacy inventories without a checksum remain readable; refetching upgrades them.

With `fetch --cache-inventory`, raw publication is followed by cache import, not a cross-store transaction. A valid raw pair can be imported offline after a cache failure; a torn pair requires a fresh full capture. The lock order is inventory metadata, cache acquisition, then cache metadata; ledger locks are never held during cache acquisition/import. `sync` takes the inventory metadata lock before the ledger lock and releases the ledger lock before advancing the sync checkpoint. See [inventory recovery](evidence-reference.md#reusing-inventory-bodies-offline).

Corpus progress references only published immutable evidence. A killed runner can leave an old cursor or a pending plan, never a committed reference to unfinished evidence. See [corpus recovery](evidence-reference.md#frozen-corpus-acquisition) for retries, locks and validation.

Sync writes the ledger before advancing its fetch checkpoint. If checkpoint publication fails, rerunning sync safely replays that inventory while retaining local decisions and notes. This does not make a GitHub crawl a globally consistent snapshot. The [evidence cache](evidence-reference.md#publication-and-recovery) has its own manifest-last publication protocol.

Batch briefing has a separate tracked checkpoint under `data/<owner>/<repo>/briefed-batches/`. Finish the Markdown brief before running `bin/batch --mark-briefed`; the script checks its path and that it is nonempty, then atomically saves the batch membership and brief path. It cannot judge whether the prose is finished. If a brief was written but the checkpoint was not, mark the same batch after inspection. If the checkpoint exists, the next `--unbriefed` selection skips its members even after the disposable batch files are deleted. This records screening, not review or approval.

## Action proposals, labeling and automation settings

Each action proposal is one checksummed file, `data/<owner>/<repo>/action-proposals/<kind>-<number>.json`, replaced atomically under a per-item lock with its earlier versions kept in its history. A proposal names the request ID of its write receipt in `data/<owner>/<repo>/writes/`; while that receipt exists for a pending proposal, editing, rejecting and replacing are refused until the attempt is inspected. A write whose outcome could not be confirmed is saved as `uncertain`; nothing retries it automatically. Viewed and dismissed marks are presentation state in the ignored `data/<owner>/<repo>/local/action-proposals-state.json`.

The labeling pass keeps what it last observed and which labels it manages per item in `data/<owner>/<repo>/local/item-labels-state.json`, and appends every outcome to `item-labels-outcomes.jsonl` beside it. Both are in the ignored `local/` directory, so they belong to one checkout and are not shared through Git. An item is marked pending before its first label write; if the pass stops there, later passes skip that item until `bin/item-labels reset-item` previews and records the live labels.

Whether labeling is on, and which action types may execute directly, are saved per repository in `config/label-application.json` and `config/action-automation.json`. Each is replaced atomically under its own lock after its preview hash is rechecked.

The current-item index is derived and rebuildable; fixed snapshots do not depend on it. REST page jobs and cooldowns are acquisition state, not completed evidence. See [index recovery](evidence-reference.md#current-item-index-and-offline-recovery) and [job and cooldown recovery](evidence-reference.md#resumable-page-jobs-and-cooldowns) for the separate locks, validation and retry boundaries.

## Group exports

`group export --output PATH` publishes one complete JSON or Markdown file with the same flushed atomic replacement primitive. Acquisition and rendering happen before opening the destination; publication is serialized by a lock beside that output, without holding the group or ledger lock during requests. Readers see the old or new complete file. An interrupted pre-replacement write leaves the old file (or no output for a first export); a flush error after replacement can leave the new complete file. Inspect it before retrying. No multi-file journal or separate recovery command is needed: rerun the export if necessary, using a fixed snapshot when reproducing an earlier evidence selection.

Temporary files and locks stay in a self-ignoring `local/` directory beside the output. Final output symlinks and symlinked local storage are refused. Parallel exports targeting the same path publish complete files one at a time; the last completed publication wins, without an expected-output revision guard. Use different filenames to retain both observations. Shell redirection of stdout does not get these guarantees; use `--output` for an existing file you want preserved if acquisition fails. Group exports never change group membership, local decisions, or approvals.

## Group transactions

Group membership and metadata writes use the group lock and atomic replacement. The TUI passes the loaded revision; CLI callers can pass `--revision` to reject stale updates. Ordinary edits retain legacy structured fields in existing group files, while new exports omit them from maintainer handoffs.

## Closed-PR watches

Watch snapshots commit before the checksummed watch record references them. Enrollment and polling hold the per-watch lock; interruption can leave an unreferenced snapshot or a running poll marker, but not a watch reference to unpublished evidence. Keep the whole ignored cache when backing up watches. See [closed-PR watches and appeal review](appeal-evidence.md) for polling, recovery and offline readers.

## External closure history

`cache closure-import` atomically appends to `data/<owner>/<repo>/external-closures/pr-N.json` under a per-PR lock. Failure before replacement leaves the old record; failure after replacement may leave the new record visible, so inspect it before retrying. The tracked history still depends on ignored cache evidence: back up both. See [external closures](external-closures.md) for import and correction commands.
