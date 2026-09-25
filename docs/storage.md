# Local storage and recovery

Transactional data such as the ledger, groups, cache metadata, watches and imported closure histories is written by staging a temporary file, flushing it, replacing the destination, then flushing the directory entry. Readers see the previous complete file or the new one, never a partly rewritten one. Reports and CSV exports are not covered by this guarantee. This depends on a filesystem that supports POSIX locks, atomic rename, and `fsync`.

Locks coordinate cooperating processes sharing one filesystem, not separate clones, Git merges, or future GitHub executors. The ledger itself keeps its single-writer workflow: nothing here detects two people editing the same decision, and no GitHub writes are introduced.

## Interrupted writes and fetches

Locks and temporary files live in a `local/` directory beside the data being written. It creates its own `.gitignore` containing `*`, so these files are ignored even in an existing install without updated installer rules. That directory must be on the same filesystem as its destination. Do not remove or replace lock files while any writer may be running: the stable lock file is what makes waiting writers cooperate.

An interruption before replacement leaves the old complete file; an interruption after replacement can leave the new complete file. A reported filesystem error after replacement may therefore mean the change is already visible: inspect the file before retrying. A killed process can leave ignored temporary files, but they are never read as data. The OS releases its lock when the process exits. Unused temporary files can be cleaned up when all writers are stopped; keeping them does not affect recovery.

`fetch` and `sync` share an inventory lock, which acquisition holds while reading GitHub. Inventory metadata includes a checksum of the raw JSONL. Metadata is published first; a crash between its publication and the raw file's publication leaves a checksum mismatch, which `sync` refuses. Recover with `bin/fetch --full`, then `bin/sync`. Legacy inventories without a checksum remain readable; refetching upgrades them.

With `fetch --cache-inventory`, raw publication is followed by cache import, not a cross-store transaction. A cache failure can leave a valid raw pair: retry `cache import-inventory` offline after resolving that failure. A torn raw pair instead requires `fetch --cache-inventory --full` (and the original `--host`, if applicable). Import validates versioned provenance/checksum without advancing sync markers or changing decisions. The lock order is inventory metadata, cache acquisition, then cache metadata; ledger locks are never held during cache acquisition/import. See [inventory scope and limits](evidence-reference.md#reusing-inventory-bodies-offline).

Corpus plans and progress use separate checksummed version-1 artifacts under `cache/corpora/`. The immutable plan is published first; absent progress means pending, not complete. A per-corpus runner lock covers the run, nesting only cache acquisition/publication locks (never inventory or ledger locks). Progress is atomically replaced after immutable evidence is published, so a killed runner can leave an old cursor but never a committed reference to unfinished evidence. Explicit retry reuses eligible cache evidence; status reads atomic checkpoints without acquiring the runner lock. Corruption/future versions/missing references require inspection or backup restore, not silent reset. See [corpus recovery limits](evidence-reference.md#frozen-corpus-acquisition).

Sync writes the ledger before advancing its fetch checkpoint. If checkpoint publication fails, rerunning sync safely replays that inventory while retaining local decisions and notes. This does not make a GitHub crawl a globally consistent snapshot. The [evidence cache](evidence-reference.md#publication-and-recovery) has its own manifest-last publication protocol.

Its current-item index shares the cache metadata lock for publication, reads, and rebuild. A durable invalidation marker precedes each new snapshot commit; the derived index is replaced afterward. Missing/interrupted index state rebuilds offline; corrupt indexes require explicit `bin/cache rebuild-index`, which validates all history before replacing derived data. Fixed snapshots never depend on those pointers. See [cache index recovery](evidence-reference.md#current-item-index-and-offline-recovery) for identity/version refusal, restore instructions, and the limits of directory-change detection.

REST page jobs and cooldowns use the separate acquisition lock. Page objects become durable before atomic checkpoint replacement; completed evidence is committed before job completion state. After interruption, rerun cache-preferred acquisition to revalidate/resume eligible pages, or inspect `bin/cache jobs` offline. Job state is not evidence completeness. Cooldowns survive process exit and block new acquisition requests until their deadline. See [job/cooldown recovery](evidence-reference.md#resumable-page-jobs-and-cooldowns) for corruption, restore, and retry boundaries.

## Group exports

`group export --output PATH` publishes one complete JSON or Markdown file with the same flushed atomic replacement primitive. Acquisition and rendering happen before opening the destination; publication is serialized by a lock beside that output, without holding the group or ledger lock during requests. Readers see the old or new complete file. An interrupted pre-replacement write leaves the old file (or no output for a first export); a flush error after replacement can leave the new complete file. Inspect it before retrying. No multi-file journal or separate recovery command is needed: rerun the export if necessary, using a fixed snapshot when reproducing an earlier evidence selection.

Temporary files and locks stay in a self-ignoring `local/` directory beside the output. Final output symlinks and symlinked local storage are refused. Parallel exports targeting the same path publish complete files one at a time; the last completed publication wins, without an expected-output revision guard. Use different filenames to retain both observations. Shell redirection of stdout does not get these guarantees; use `--output` for an existing file you want preserved if acquisition fails. Group exports never change group membership, local decisions, or approvals.

## Group transactions

Group membership and metadata writes use the group lock and atomic replacement. The TUI passes the loaded revision; CLI callers can pass `--revision` to reject stale updates. Ordinary edits retain legacy structured fields in existing group files, while new exports omit them from maintainer handoffs.

## Closed-PR watches

Watch snapshots commit before the checksummed watch record references them. Enrollment and polling hold the per-watch lock; interruption can leave an unreferenced snapshot or a running poll marker, but not a watch reference to unpublished evidence. Keep the whole ignored cache when backing up watches. See [closed-PR watches and appeal review](appeal-evidence.md) for polling, recovery and offline readers.

## External closure history

`cache closure-import` atomically appends to `data/<owner>/<repo>/external-closures/pr-N.json` under a per-PR lock. Failure before replacement leaves the old record; failure after replacement may leave the new record visible, so inspect it before retrying. The tracked history still depends on ignored cache evidence: back up both. See [external closures](external-closures.md) for import and correction commands.
