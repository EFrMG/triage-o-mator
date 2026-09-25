# Evidence commands and storage reference

Technical details for cache acquisition, readers, coverage, and recovery. Start with [the short evidence guide](evidence.md) for the operator workflow.

## Offline source search

Search an explicit immutable snapshot or the pinned member results of a frozen corpus:

```sh
bin/cache search --snapshot SNAPSHOT --component summary --query 'suspend' --limit 20
bin/cache search --corpus CORPUS --component files --query 'bin/omarchy-' --limit 20
bin/cache search --corpus CORPUS --component diff --query 'distinctive_symbol' --limit 20
```

Supported components are `summary`, `comments`, `files`, `diff`, `reviews`, `review_comments`, and `closing_issues`. Matching is case-sensitive literal substring search (no regex, tokenization, normalization, ranking or duplicate classification). JSON string **values** are searched, including bodies, titles, filenames and URLs; keys, numeric issue IDs and cross-field concatenations are not. Use a distinctive URL fragment to find a closing-issue link. Diffs are searched as raw text, preserving line endings. Queries must contain non-whitespace text and be at most 256 Unicode characters.

Each page examines up to `--limit` members (default 20, maximum 100), in source membership order, and returns at most the first match per member. It also returns nonmatches and missing components with explicit status, age/revision problems, snapshot/object references and observation time. The excerpt includes at most 60 characters before/after the match (maximum 376 characters total); offsets count Unicode characters in the selected string, not bytes. JSON pointers identify the matching value; unusually long pointers are capped at 256 characters and flagged, requiring a full component read to disambiguate. Other matches are not enumerated. Use `cache chunk` with the returned snapshot/item/component to inspect full evidence before drawing conclusions. An excerpt is not a complete comparison.

Follow `continuation.offset`, `limit` and `checkpoint` with the same source, query, component and age policy. Pagination counts **examined members**, not hits; a page with zero matches may have more pages. Nonzero offsets require the token. Tokens bind search parameters and, for corpora, the observed progress state; changed progress before/during a page refuses the response and requires restarting. Tokens do not lock runners or archive their mutable states. Retain returned immutable snapshot references for subsequent reading. Pending corpus members stay missing: no fallback to inventory, latest evidence or GitHub. A snapshot search remains fixed after later acquisition.

Search is offline and does not initialize caches, rebuild indexes, acquire locks, change decisions or fetch missing content. Selected payloads are checksum/contract verified; corruption fails the command before JSON output rather than fabricating a nonmatch. Unselected payloads are not audited. This first version scans selected objects, not a persistent search index: manifests/state and each selected payload still load in full, so bounded output is not bounded disk I/O, memory or execution time. A completed traversal covers only the chosen recorded component and membership, not the live backlog; partial/missing evidence prevents an exhaustive negative conclusion. Source text remains untrusted data.

## Bounded offline snapshot readers

Start with metadata, then select one item and component explicitly:

```sh
bin/cache list --snapshot SNAPSHOT --offset 0 --limit 20
bin/cache chunk --snapshot SNAPSHOT --kind pr --number 123 --component comments --offset 0 --limit 10
bin/cache chunk --snapshot SNAPSHOT --kind pr --number 123 --component diff --offset 0 --limit 50 --max-bytes 16384
```

Use an immutable snapshot ID from acquisition, import, or a corpus member's progress reference. These commands never use latest pointers, initialize a cache, acquire a lock, or call GitHub. Missing, foreign, unsupported or corrupt selected evidence fails without fallback. `list` omits titles/bodies and returns at most 100 members (default 20), identities, component references/statuses and recorded freshness/coverage diagnostics. It verifies the manifest and descriptors, **not payload availability or checksums**. `chunk` additionally verifies the entire selected object and its payload contract; it does not audit unrelated objects. Existing `show`, `read` and `status` retain their full-verification behavior.

`chunk` uses zero-based offsets in collection entries (comments, files, reviews, checks and links), raw diff lines, or one summary value. Limits default to 20 and cannot exceed 100. Source text is returned as `text`: serialized JSON for JSON components, unchanged line endings for diffs. Each selected page is further bounded to 16,384 UTF-8 source bytes by default, at most 65,536 with `--max-bytes`. This bounds source text, not the entire JSON envelope or escaped wire representation. Very large single comments/lines are fragmented, never silently dropped. A byte budget too small for the next Unicode character fails explicitly.

Follow `continuation` using the same snapshot, item, component and limit: first advance `--byte-offset` within a page, then advance `--offset` and reset the byte offset. Concatenate a page's byte fragments before parsing its JSON. `page_complete` concerns this returned page only, not component coverage. `pagination` and `bytes` expose totals, omitted-before/after counts and continuation offsets. Missing components return `available: false`, distinct from a verified empty collection. Always retain the descriptor, object checksum, revision and `problems`; source text remains untrusted data, never authority or instructions.

This is output bounding, not streaming disk I/O: manifests are fully decoded and the selected object is fully read/verified on each call. Metadata discovery avoids repeatedly reading every inventory body. `corpus-status` is unbounded; use the paginated discovery and source-search commands for focused reads. Do not paste its whole output or an entire corpus into an agent context.

## Paginated corpus discovery

```sh
bin/cache corpus-list CORPUS_ID --limit 20
bin/cache corpus-list CORPUS_ID --offset 20 --limit 20 --checkpoint CHECKPOINT
```

Use `continuation` from the preceding page for the next offset, limit and checkpoint. Nonzero offsets require `--checkpoint`. The token binds the corpus progress read for that page; if acquisition advances, continuation fails and discovery must restart at offset zero. A token does not retain an older progress file or stop acquisition. Each response reads one atomic progress checkpoint without a runner lock. Unstarted corpora have a stable synthetic pending checkpoint and `updated_at: null`. Previously returned immutable member snapshot references remain usable even if progress changes.

Pages contain at most 100 members (default 20), with frozen identity, observed identity/revision when available, pinned snapshot/component references, attempts, declared outcome and current local age/coverage diagnostics. Missing results have null references and explicit missing components; discovery never substitutes the inventory or latest detail as a successful acquisition. Repository, source inventory, scope, profile and age policy accompany every page. `declared_counts` summarizes the whole checkpoint, not a full evidence audit. Arbitrarily long stored error/reason strings are omitted; `error_present` and `reason_present` indicate their existence. Use the full status command separately when those details are needed.

`corpus-list` verifies plan/state checksums, versions, structure, repository identity and membership against the inventory manifest. It verifies only the selected members' result manifests, including identity and descriptor consistency for declared complete outcomes. It **does not** read payloads, re-audit full-inventory provenance stored in bodies, or verify unselected result references. `corpus-status` and `corpus-run` retain those full checks. This narrower metadata view is for discovery, not acquisition validation or execution authority; a missing/corrupt unselected payload can remain undiscovered until selected or audited. Reads stay offline, do not initialize storage or rebuild indexes, and refuse invalid selected metadata without fallback.

## Basic cache commands

This release provides versioned evidence storage, **read-only acquisition for explicitly selected items and frozen inventory corpora**, and shared readers. `enrich-one`, `batch`, `similar --enrich`, and `group export --enrich` can opt in. Default TUI enrichment still uses its existing path; the TUI can read fixed cached batches without fetching missing evidence. No cache command changes the ledger or authorizes actions. Use `f` for budgeted inventory capture and corpus acquisition.

From an install:

```sh
bin/cache status
bin/cache usage
bin/cache init --dry-run
bin/cache init
bin/cache fetch --kind pr --number 123 --profile pr-context
bin/cache read --kind pr --number 123 --profile pr-context
bin/cache show <snapshot-sha256>
bin/enrich-one --kind pr --number 123 --cache-mode offline
```

`init --dry-run` creates no install data or locks. Initialization downloads nothing and leaves repository identity unbound. `status` and `show` validate manifests and referenced payload checksums; this can be expensive for a large corpus.

`usage` counts local cache files and logical/allocated bytes offline. Before writing source objects and snapshot manifests, acquisition checks a 5 GB allocated-byte budget with 256 MB reserved for publication. A request that would exceed it stops with retained evidence intact. Small metadata and temporary files are outside this preflight guard, so this is a practical acquisition budget rather than a strict filesystem quota. Item sizes vary; five thousand items are not guaranteed to fit. Retained historical evidence counts toward usage and is not silently pruned.

## Acquisition and read modes

`fetch` initializes the cache if necessary and uses explicit-host REST GETs, plus one fixed read-only GraphQL query for closing-issue links. GraphQL queries use HTTP POST; the tool does not expose mutations or arbitrary query text. Authentication is managed by `gh`; credentials and its stderr are not copied into evidence. `read`, `show`, `status`, `init`, and `rebuild-index` are offline. Reads of an absent cache report missing evidence without creating it; latest-item reads may rebuild disposable indexes in an existing cache.

Six selected-item profiles are implemented:

| Profile         | Requested components                                                                                                                             |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `discussion`    | Summary/body and conversation comments; the default for issues and PRs                                                                           |
| `pr-context`    | Discussion plus files, submitted reviews, and inline review comments                                                                             |
| `pr-code`       | Discussion plus files and raw diff; used by cached `enrich-one --diff`                                                                           |
| `pr-comparison` | PR context plus raw diff, head-SHA check suites/runs and commit statuses, and structured closing-issue links                                     |
| `backlog`       | Summary and comments for open items, plus file lists, diffs, and structured closing-issue links for PRs; no review activity, checks, or timeline |
| `closure-watch` | Summary/body, issue discussion and timeline; explicit closed-PR watch capture                                                                    |

`pr-comparison` requests the principal inputs for a PR comparison; its name does **not** promise every component succeeded or establish duplication. Timeline events are available through `closure-watch`. Resolved-thread state and surrounding source code are not collected. PR-only components on issues are explicitly not applicable. File responses retain paths, rename information, and supplied patches; omitted patches are reported as partial code evidence, including possible binary/oversized changes. Neither a file list nor test-file presence proves test coverage.

`bin/cache fetch` defaults to `--mode cache-preferred`. A complete observation within `--max-age` (default 86,400 seconds), consistent with its latest recorded revision, is reused without network access. Missing, incomplete, or expired evidence triggers acquisition; independently fresh components may be reused after reading current metadata. `--mode refresh` forces all components in the selected profile to be fetched again. `bin/cache read` is always offline and displays available payloads with per-component `problems`, including missing and stale evidence. Offline/cache-hit freshness is relative to the **latest recorded revision**, not a claim that GitHub has not changed.

```sh
bin/cache fetch --kind pr --number 123 --profile pr-context --mode refresh --request-budget 50
bin/cache read --kind pr --number 123 --profile pr-context --max-age 3600
bin/enrich-one --kind issue --number 456 --cache-mode cache-preferred
bin/cache fetch --kind pr --number 123 --profile pr-comparison --request-budget 100
bin/enrich-one --kind pr --number 123 --diff --cache-mode offline
```

`enrich-one --cache-mode offline|cache-preferred|refresh` preserves available legacy body, comment-array, PR-stat, and requested `diff_text` fields and adds `evidence` diagnostics; state distinguishes merged from closed. Missing payloads do not get fabricated empty compatibility fields. Partial payloads, including diffs, may be returned, so consumers must inspect `evidence.problems`. Cached `--diff` selects `pr-code` for PRs, without fetching checks or GraphQL links; on issues it retains discussion-only behavior. Omitting `--cache-mode` retains the old online path; no automatic fallback crosses the offline boundary. `--host` for enrichment requires cached mode.

Acquisition is sequential, with a default 100-request budget and 45-second timeout per request. HTTP 403/429 or an exhausted rate limit stops further requests and persists a cross-run cooldown. There is no automatic retry loop or background scheduler: inspect the failure and retry explicitly after the indicated time. Budget exhaustion and failed later pages retain partial evidence, never a false completion. API page URLs are constructed locally; fetched text and links cannot redirect requests to arbitrary endpoints.

Completed components are checkpointed as immutable snapshots. REST discussion/file/review collections also persist validated pages in disposable jobs, described below. Summary status stays partial until a final metadata recheck succeeds. If the base/head, repository scope, item update revision, or observed counts change during acquisition, completed dependent components become partial and retain their original revisions. An inaccessible final read does not turn an item into a verified closed/deleted item. An initial identity/summary failure exits without inventing an item snapshot.

The reader uses a rebuildable current-item index to select the latest observation, including partial observations, rather than hiding a new failure behind an older complete snapshot. It then verifies the selected snapshot and its objects. A narrower refresh retains previously acquired components with their original observation metadata. Check-suite and GraphQL acquisition restart at the component level; corpus runs retain per-item progress.

## Resumable page jobs and cooldowns

```sh
bin/cache jobs
bin/cache fetch --kind pr --number 123 --profile pr-context --mode cache-preferred
```

`jobs` validates local job records and page objects and prints component identity, state, saved-page count, revision, and any persisted cooldown. It never fetches GitHub, initializes an absent cache, or promotes pages into evidence. It takes the acquisition lock, so it waits for an active local acquisition rather than reporting live progress. A job is not a batch, immutable evidence snapshot, approval, or whole-backlog job.

`comments`, `files`, `reviews`, and `review_comments` have one version-1 `evidence-page-job` per repository/item/component. Each job binds stable repository/item IDs, the locally constructed endpoint, full recorded item revision, base/head repository scope, expected count when available, and state (`collecting`, `complete`, or `invalidated`). Each validated page records its sequential number, next-page presence, observation time, safe ETag if supplied, and a checksummed JSON object reference. Source text cannot supply another endpoint. Invalid arrays, duplicate identities, and failed requests do not advance the checkpoint.

Page bytes are flushed to the shared object store **before** atomic job replacement. An interruption leaves the previous or new complete checkpoint; unreferenced objects/temporary files are harmless and no page is reconstructed from them. Killing acquisition inside a component can leave useful job pages while the latest immutable snapshot still reports that component missing/failed. Ordinary evidence readers use snapshots only, not unfinished jobs. A later explicit cache-preferred run may resume those pages after re-reading repository/item metadata. The final snapshot commits before jobs are marked complete; a crash between those writes can leave a collecting job even when a later snapshot is complete. Job state alone is never coverage evidence.

Resume requires matching identity, full revision, repository scope, expected count, and all saved pages within `--max-age`. A mismatch or `--mode refresh` starts at page one, without changing historical snapshots. `refresh` still refuses corrupt/unsupported job records instead of silently overwriting them. Before reusing pages:

- PR files revalidate the last saved page; earlier pages are reused under the recorded commit/repository/count binding. A changed boundary restarts the collection. In the four-page synthetic test, resuming three saved pages needs only boundary page 3 and remaining page 4, saving two page requests.
- Discussion, submitted reviews, and inline review comments revalidate **every** saved page, because they can change independently of summary revisions. Where an ETag exists, this uses conditional GET; a bodyless `304` reuses verified saved bytes. Otherwise the full returned page must match. Changed data or pagination restarts from page one. This saves response bytes when validators match, not necessarily HTTP requests; every request still consumes the tool's local budget.
- Terminal saved pages are fetched unconditionally to inspect current pagination even when their body might be unchanged. A failed revalidation/budget stop preserves the job but does not count unverified pages as complete. Reused component data retains its earliest page observation time.

The same final metadata/count/scope recheck still applies. Verified drift invalidates participating jobs; failed final reads leave them resumable, not approved or finally verified. GitHub reads are not an atomic snapshot, and this does not promise detecting every upstream edit between observations. File resume relies on recorded commits and boundary validation, not a content-addressed GitHub page API. Raw diffs, check suites/runs/statuses, and closing-issue GraphQL connections retain component-level retry only.

Cooldowns are version-1 checksummed `evidence-cooldown` records, scoped to **this install's repository cache**, not a global account/token limiter. HTTP 403/429 (including ambiguous permission failures), successful responses with zero remaining quota, and GraphQL exhaustion persist a retry deadline. Numeric/date `Retry-After`, primary reset time when remaining is zero, and GraphQL reset time are honored when parseable. The deadline is at least 60 seconds; repeated explicit throttled attempts within an hour of the previous deadline use exponentially increasing fallback delays capped at an hour, never shortening a supplied later deadline. Malformed or missing guidance uses that fallback. Request-budget exhaustion and ordinary 5xx errors do not create rate-limit cooldowns.

Before any cached-acquisition network request, the saved deadline is checked. A later process—including `refresh` or another selected item—fails before calling `gh` while the cooldown is active. Offline reads and wholly satisfied cache hits remain usable. Once the deadline passes, retry is allowed, **not scheduled automatically**. Separate installs, repository caches, and legacy online commands do not share this limiter. A corrupt cooldown fails closed for acquisition; never delete one merely to bypass throttling. No credentials, `gh` stderr, or arbitrary response headers are persisted.

Stop acquisition before repairing/restoring job files. Preserve the affected files for inspection; unsupported versions and foreign identities require explicit reconciliation. For an irreparably corrupt disposable page job, an operator can move only that named job out of `cache/jobs/` after inspection, then retry from page one; source objects and immutable evidence are not deleted or rewritten by a retry. There is no automatic pruning or force-reset command. All job/cooldown state remains ignored and uses the acquisition lock; other clones and manual editors do not participate.

Source contracts: [GitHub conditional requests and rate-limit guidance](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api). The transport handles a valid bodyless conditional `304` even when `gh` exits nonzero, consistent with [the CLI response handler](https://github.com/cli/cli/blob/trunk/pkg/cmd/api/api.go).

## Current-item index and offline recovery

`cache/indexes/current.json` is a checksummed, version-1 `evidence-current-index`, scoped to the cache's host/repository identity. It maps `kind:number` to accumulated stable item IDs, the latest snapshot ID, and observation interval. Selection orders by completion time, then start time, then snapshot ID; completeness is **not** a ranking input. Out-of-order publication cannot hide a newer partial observation. Stable-ID contradictions and aliases across historical items fail before new publication or during rebuild. Unknown IDs in older snapshots remain unknown in returned evidence, even if the index knows them from other observations.

The index is derived metadata, not evidence, approval, or a search index over source text. Ordinary lookups verify its version, identity, checksum, and entry contracts, then load and fully verify only the selected snapshot and its referenced objects. An absent item requires no object reads. Explicit `--snapshot` and `show` reads bypass the index entirely. Corrupt selected evidence fails without falling back to an older complete observation.

```sh
bin/cache rebuild-index
bin/cache --host enterprise.example rebuild-index
```

Rebuild verifies **all** snapshot manifests and referenced objects offline, reconstructs pointers, and atomically replaces only the derived index. It never changes snapshots, source objects, ledger decisions, or GitHub. A missing index is rebuilt automatically on the next latest-item read/publication; interrupted publication and changed snapshot-directory metadata also trigger automatic rebuilding. A corrupt index instead fails with the explicit recovery command above. Recognizable unsupported index versions and conflicting repository identities are refused even by rebuild; preserve those files and reconcile the version/namespace rather than forcing adoption. Corrupt history makes rebuild fail without replacing the previous index.

Publication, latest-item reads, and rebuild share the cache metadata lock. New objects become durable first; before publishing a new manifest, the writer flushes `indexes/current.pending`. After committing the immutable manifest, it atomically publishes the updated index, then removes and flushes the pending marker. A killed writer cannot leave an old index silently hiding a committed new snapshot: the next reader sees the marker and rebuilds. If interruption happened before the manifest commit, only earlier committed history is indexed. Retrying publication is safe. Local locks do not coordinate other clones, manual editors, or older writers.

The index records the snapshot directory's device/inode and nanosecond modification/change timestamps. This cheap invalidation hint detects ordinary file additions/removals and restored/replaced directories; it is not a cryptographic proof of the directory's contents. Stop writers while copying/restoring the cache and explicitly rebuild afterward. In-place corruption of an **unselected** historical file/object is intentionally not detected by each lookup; use `status` or `rebuild-index` for a full audit. Selected evidence is always checked. Checksums are not authentication against deliberate local modification.

## Fixed snapshots and offline batches

Use `--snapshot` to read a specific immutable observation instead of searching for the latest one:

```sh
bin/cache read --kind pr --number 123 --profile pr-code --snapshot <snapshot-sha256>
bin/enrich-one --kind pr --number 123 --diff --cache-mode offline --snapshot <snapshot-sha256>
bin/batch 25 --kind pr --diff --cache-mode offline
bin/read-batch <batch-id> --list
bin/read-batch <batch-id> --number 123
```

Fixed snapshot selection requires offline mode. Invalid/missing snapshots, wrong repositories, corrupt objects, and items outside the snapshot fail explicitly, without falling back to latest or GitHub. Missing components within a valid snapshot still return available data with diagnostics. Freshness is checked against that snapshot's recorded revision and current time, not newer cache observations or current GitHub state.

`batch --cache-mode offline|cache-preferred|refresh` uses the same reader (`discussion`, or `pr-code` with `--diff`). Each item embeds its selected snapshot ID, repository identity, component references, revisions, observation times, and coverage diagnostics alongside available compatibility body/comments/diff fields. A batch may reference different snapshots per item; it is not an atomic GitHub observation. `--snapshot ID` pins every selected item to one snapshot and fails if any selected item is absent. It does not change selection: items still come from the untriaged open ledger/group filters.

The embedded context stays unchanged when cache history refreshes. `read-batch` reads the committed packet, never the live cache, and shows coverage problems. Embedded text remains readable even without the cache, but the working packet is **not** a portable full-evidence archive: file/check/review objects and snapshot manifests still need their cache. Copy both working files together when moving a batch.

The TUI also reads committed pairs and shows fixed-packet diagnostics. It does not replace missing packet evidence with session content or a lazy diff download. Its default batch creation and item enrichment remain legacy online reads; opting into cached batches currently uses the CLI. Coverage diagnostics displayed from a packet were recorded when it was created; they are not recalculated to imply live freshness.

`--request-budget` on `batch` is per item (default 100); `--max-age` and explicit `--host` have the same meaning as on `enrich-one`. A rate-limit stop aborts batch creation before starting another item, retaining acquired cache checkpoints. Partial/missing evidence can form a completed local packet but never implies completed evidence acquisition, review, or permission to act.

## Similarity and group consumers

```sh
bin/similar --kind pr --number 123 --diff --cache-mode offline
bin/similar --kind issue --number 456 --enrich --cache-mode cache-preferred
bin/group export GROUP_ID --diff --cache-mode offline --format json --output data/<owner>/<repo>/exports/review.json
bin/group export GROUP_ID --enrich --cache-mode refresh --output data/<owner>/<repo>/exports/review.md
```

Both commands accept the same `--cache-mode`, `--host`, `--max-age`, `--request-budget` (per selected item), and offline `--snapshot` options. Pass `--enrich`, or `--diff` which implies enrichment. Without cache mode, enrichment retains legacy online behavior. Without enrichment, similarity and group exports remain ledger-only and offline. `similar --pairs` and `--query` do not support enrichment; incompatible flags are rejected rather than ignored.

Similarity **selection and scores still use ledger titles**, not cached bodies, files, or checks. Enrichment reads the selected item and each returned candidate using `discussion`, or `pr-code` for PRs with `--diff`. Updated cached titles do not recompute the title score. Each result retains its own fixed snapshot/component references and explicit problems; this does not make the candidate list exhaustive or establish duplication. Checks and reviews require explicit comparison-profile reads through `bin/cache`; closing links are also available in a completed backlog snapshot. None of these components changes the title score.

Group JSON packets keep their schema-1 envelope, group revision/membership, and local decision fields, adding per-item `evidence` metadata. Markdown packets show snapshot IDs, source host/repository, recorded revisions, observation times, and gaps. Missing comments are not presented as an empty discussion. Members missing from the ledger stay included and marked, even when cache evidence exists. Cross-reference links use the selected cache host; legacy mode remains github.com-only.

`--snapshot ID` requires every selected item to be present in that snapshot, including similarity candidates or all group members; it never narrows selection or silently fetches an omitted item. Without it, each item chooses its own observation. A saved export does not follow future cache/group updates and its embedded text remains readable without the cache. JSON retains full component references; Markdown is a human-readable projection. Neither is a durable self-contained archive of every referenced evidence object, nor an authorization to act. They are local snapshots of an observation interval, not an atomic view of GitHub and the ledger together.

A rate-limit stop aborts further members/candidates and publishes no completed output; successful cache checkpoints remain. For group `--output`, acquisition and rendering finish before atomic file replacement. A failure before replacement leaves any previous export intact; see [single-file export recovery](storage.md#group-exports). Stdout is assembled only after all selected items finish, but shell redirection is outside the tool's atomic-file protocol.

## Reusing inventory bodies offline

```sh
bin/fetch --cache-inventory --full
bin/sync
bin/cache read --kind pr --number 123
# Retry an import after interrupted cache publication, without fetching again:
bin/cache import-inventory
```

`fetch --cache-inventory` verifies repository identity, captures paginated list records with an explicit host (default `github.com`), validates the inventory, publishes the raw metadata/data pair, then imports an immutable summary snapshot. It uses ordinary full/incremental selection and sync checkpoints. `--host HOST` and `--request-budget N` require this opt-in flag; the budget includes repository verification. The cached transport obeys persisted cooldowns and per-request timeouts. Ordinary fetch/enrichment defaults remain unchanged.

Each summary retains the fetch interval, endpoint, full-open or incremental-updated scope, pagination declaration, raw checksum, source IDs, body, labels, author, timestamps, and source state. These are **partial list observations**, not verified detail: missing discussion stays missing, zero listed comments does not manufacture fetched discussion, and PR revisions/checks/mergeability remain unknown. Closed PRs expose `state: unknown` and `source_state: closed`, because the projection does not establish whether they merged. The [issues API returns issue IDs rather than PR IDs](https://docs.github.com/en/rest/issues/issues#list-repository-issues); PR list IDs remain source data and do not bind PR-detail identity.

Offline reads/enrichment reuse available bodies with explicit gaps and zero GitHub requests. Cache-preferred discussion/code reads still acquire missing detail; this slice does **not** save those verification requests. A newer inventory can become the latest partial observation even when older detail exists; fixed-snapshot readers retain that older evidence. Reimporting an older inventory does not change its observation time. Empty inventories create no item snapshot and infer no closures/deletions. Paginated inventories are observation intervals, not atomic or access-independent censuses.

`cache import-inventory` is offline and idempotent; use the capture's `--host`. It validates version, scope, identity, counts, IDs, URLs and raw checksum. Legacy raw files remain sync-compatible but cannot be imported as verified evidence: refetch with `--cache-inventory`. Bodies and list IDs stay out of the ledger. Acquisition failure preserves the previous raw pair; interrupted raw publication is rejected by checksum. If raw publication succeeded but cache publication failed, preserve the files and retry import after resolving the cache problem. Neither import nor fetch advances sync checkpoints. Existing cache locks and manifest-last recovery apply; the cache is ignored and is not a portable archive. Inventory pages currently restart on failure rather than resume.

Source contracts: [GitHub CLI HTTP headers and explicit hosts](https://cli.github.com/manual/gh_api), [issue comments](https://docs.github.com/en/rest/issues/comments#list-issue-comments), [PR files and the 3,000-file cap](https://docs.github.com/en/rest/pulls/pulls#list-pull-requests-files), [submitted reviews](https://docs.github.com/en/rest/pulls/reviews#list-reviews-for-a-pull-request), [inline review comments](https://docs.github.com/en/rest/pulls/comments#list-review-comments-on-a-pull-request), [inventory bodies](https://docs.github.com/en/rest/issues/issues#list-repository-issues), and [rate-limit guidance](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api).

## Frozen corpus acquisition

Create a full inventory first with `bin/fetch --cache-inventory --full`. Its stderr reports the imported snapshot ID; `bin/cache import-inventory` also returns that ID as JSON without another request. Then:

```sh
bin/cache corpus-create --snapshot <inventory-snapshot-id>
bin/cache corpus-status <corpus-id>
bin/cache corpus-run <corpus-id> --request-budget 100
# Inspect or explicitly resume later:
bin/cache corpus-status <corpus-id>
bin/cache corpus-run <corpus-id> --request-budget 100
```

For the TUI's `backlog` profile, add `--bulk` to `corpus-run` to use the batched path. The TUI supplies it automatically. The fixed GraphQL operations fetch up to 80 item summaries, comments, PR file lists, and structured closing-issue links per call; a second operation rechecks revisions and counts. One Git transfer fetches the corresponding PR heads, and local `git diff` produces text hunks. An HTTP 502 splits only the affected batch into smaller fixed-query requests; later batches start at 80 again. The shared budget counts failed calls, split requests and Git transfers. GraphQL pages beyond the first 100 comments, files, or closing links, missing commits, unsupported paths and binary or omitted hunks remain explicit gaps. Focused `cache fetch` can fill those gaps. Git writes fetched objects to the target checkout's object store, outside the install cache budget.

For an update, `corpus-create --snapshot NEW_INVENTORY --scope open-items --profile backlog --reuse-corpus PRIOR_CORPUS` freezes the prior download’s selected member references alongside the new membership. Acquisition rechecks identity, revisions, counts and age before reuse; a newer list-only observation does not discard reusable details. Previous plans and results remain unchanged.

Creation is offline. The default is `--scope open-prs --profile pr-comparison --max-age 86400`. Other scopes are `open-issues` and `open-items`; every existing profile is available. Membership, profile and age policy are immutable and checksummed, bound to the repository and one full inventory snapshot. Identical inputs yield the same corpus ID. Changed inventory, scope or policy requires another plan; refreshing raw inventory never changes existing membership. Incremental inventories and detail snapshots are refused. An issue-only inventory can produce an empty PR selection; an entirely empty raw inventory currently has no snapshot from which to create a corpus.

`corpus-run` is explicit read-only acquisition. It uses one **shared request budget for that invocation**, including metadata rechecks and conditional requests across all members. There is no per-member budget reset or automatic retry scheduler. The ordinary path uses existing component/page checkpoints and cooldowns. The bulk path commits each member after its batch's final recheck; interrupted batches can be retried but do not reuse partial GraphQL pages. Only HTTP 502 triggers in-run batch splitting; a single-item 502 remains an error for explicit retry. Other HTTP errors, including rate limits, stop the run. Full open-item selections run PRs first, then issues, each in number order. With `--keep-complete`, pending and interrupted members go before recorded gaps and errors, so repeated failures cannot consume every resume budget before new items are attempted. Increase the budget if a batch cannot finish; per-member allowances are not implemented.

Each result pins an immutable item snapshot before advancing progress. By default, `corpus-run` skips a completed member only while that snapshot is still latest and satisfies the plan's age/revision policy. With `--keep-complete` (used by TUI `r`), it instead retains each completed pinned snapshot after validating its integrity, regardless of age or newer observations. This continues unfinished work without asserting that completed evidence is current; offline age diagnostics remain visible. TUI `d` uses the default update behavior. Once pending members have been attempted, resume revisits recorded gaps and errors; their original snapshots remain selected until replaced by a new observation. Changed inventory observations, newer detail, stale observations and gaps can cause another acquisition. The ordinary path can reuse valid components and page checkpoints; the bulk path rechecks selected items and replaces their complete observation. If the process dies after publishing evidence but before recording the result, retry may reacquire that batch. A member that closes or merges remains part of the frozen selection; its recorded state is evidence, not permission to close or reopen anything.

`corpus-status` is offline and never prints source bodies. It reports membership, per-item attempts/outcomes/snapshot references, component gaps at pinned revisions and current local age, counts, last-run budget/request count, and elapsed time at the last saved checkpoint. It reads atomic progress without waiting for the runner lock. `finished` means a pass reached the end: **check the complete/gaps/error/pending counts**, not only that status. Historical `complete` outcomes may now be stale; `current_problems` exposes that. Neither field guarantees current GitHub state or coverage of all currently open PRs. There is no whole-corpus atomic observation time.

Handled stops, per-item read/validation errors, gaps and Ctrl-C return JSON with exit status zero so callers can inspect the recorded outcome; zero does not mean full coverage. Preflight validation and unhandled storage failures exit nonzero. Consumers must inspect `progress.status`, `counts`, `current_problems` and `last_run.reason`, not infer success from the exit status alone.

Use Ctrl-C to interrupt a CLI runner; a subsequent explicit run resumes. SIGTERM/process death or storage failure can leave `running` progress, which is not proof that a process is still alive. The local runner lock prevents concurrent invocations for one corpus from duplicating work; it releases on process exit. Requests and elapsed time are saved at item boundaries, so a killed run can underreport its last in-flight requests. Budgets apply per invocation, not cumulatively across crashes or separate corpora. Different corpus runners still share the cache acquisition lock and persisted cooldowns. The TUI controller below owns its subprocesses; no detached daemon, remote cancellation endpoint or policy activation is provided.

Plan and progress files live under ignored `cache/corpora/`. Full status/acquisition validation refuses corrupt, missing-reference, foreign, unsupported-version and symlinked artifacts without live fallback or automatic reset. Preserve and inspect failures; restore the relevant cache backup before retrying. Corrupt state is never automatically reset or pruned. Plans/progress are separate atomic files: a plan without progress is safely pending. Completed evidence and prior checkpoints are retained on interruption. Full status/creation scan the selected inventory and referenced snapshots. Bounded metadata, component readers and literal source search are available above.

## Corpus controls in the TUI

On a main browsing screen, `f` opens Local dataset. `d` lists the open issues and PRs, creates a frozen selection, and starts downloading supported details. Opening the menu alone makes no GitHub request. The target is the install's configured repository on `github.com`; use the CLI for other hosts or custom scopes and profiles.

`n` chooses 100, 500, 1,000, 5,000, 10,000 or all frozen members per run. The item acquisition request ceiling is estimated at 20 requests per chosen item; listing has a fixed 500-request ceiling. `r` resumes the current download, keeping completed snapshots. `d` updates the open-item membership and may refresh eligible details. The runner obeys persisted cooldowns and never retries automatically.

The menu shows one current download per repository and restores it offline after a restart. A new `d` becomes current once its plan is ready. A failed or empty listing keeps the previous download selected. Older immutable corpora remain available to the CLI for inspection.

`x` cancels the owned download. Committed plans, snapshots and page checkpoints remain. `Esc`/`f` closes the menu without stopping acquisition. Repository/install switches and terminal-program return stop owned operations; an external CLI runner is never cancelled by this menu. `j`/`k` scrolls the status, `u` measures local cache size and `y` copies the agent handoff prompt.

The menu pins operation and install identities and rejects late replies after a new operation. It refreshes local checkpoint metadata while a download runs with the menu open. The bar counts processed members, including gaps and failures; separate counts show those outcomes. Updates happen at saved item checkpoints, not for every request. After cancellation, reopening Local dataset reads its retained current checkpoint. Counts describe recorded outcomes, not audited completeness or freshness. Drafts, human review, ledger state and fixed batch context are unchanged.

For custom corpus creation, use `bin/fetch --full --cache-inventory`, `bin/cache import-inventory` when needed, and `bin/cache corpus-create` with an explicit full inventory snapshot, scope and profile. See the CLI examples above for options and provenance checks.

`bin/cache corpus-progress CORPUS_ID` is the bounded offline metadata interface. It validates checksummed plan/state structure and frozen inventory-manifest membership, but does not read evidence payloads or verify referenced result manifests. It loads whole plan/state/inventory JSON in memory, so bounded output is not constant-memory storage. Stop reasons are limited to 500 Unicode characters with `reason_truncated`; per-item errors/source text are omitted. `inspection_requests: 0` refers only to this metadata read. Run requests are in `last_run.requests`.

`bin/cache corpus-run CORPUS_ID --request-budget 100 --compact` returns this same projection after acquisition, including handled stop statuses. The default run/status interfaces still perform full verification and return detailed reports; `--compact` skips the final full audit, not the runner's existing validation/acquisition safeguards. Use `corpus-status` for a payload audit and per-member gaps, or `corpus-list` for paginated pinned references. None of these reports authorizes a triage decision or GitHub write.

## Offline catalog discovery

```sh
bin/cache discover --kind inventories --limit 20
bin/cache discover --kind corpora --limit 20
bin/cache discover --kind inventories --offset 20 --limit 20 --checkpoint CHECKPOINT
```

These commands discover IDs, not member payloads. The default window is 20 files, maximum 100; `pagination` counts artifact files examined rather than eligible inventories. `excluded` counts valid snapshots in that window without full-open-list metadata. An empty `items` page can still have a continuation. Pages follow lexicographic artifact-ID order, not chronological order; dates and member counts are shown only for examined candidates.

Inventory candidates have a validated snapshot manifest with summary-only requests, partial summary payload references and the exact full-open-list REST source on every member. This is a **metadata filter**, not proof that the referenced payloads contain valid full inventory provenance or are still available. Detail/incremental snapshots are excluded when their metadata does not match. Actual corpus creation still audits every member's source provenance and state offline. Saved-plan discovery validates plan checksums and inventory-manifest membership, not progress or source objects; choosing the plan reads its checkpoint separately. No current-item index is read/rebuilt and no cache, ledger, sync checkpoint or evidence artifact is initialized or changed.

Continuation requires the returned checkpoint beyond offset zero. It binds the repository, catalog kind and sorted artifact filenames, refusing added/removed plans or snapshots. Mutable corpus progress does not invalidate a plan-catalog token. Catalog membership is checked again before returning a page. The token is not a lock, a historical catalog archive or proof against in-place mutation: examined manifests/plans are validated on each read, and selection later validates the selected artifact again. Stop writers when manually restoring a cache.

Malformed, missing-reference, future-version, foreign and symlinked examined artifacts appear as `unavailable` rows with their IDs; they are never selectable or silently hidden. Inspect snapshot failures with `cache show ID`, and corpus failures with `cache corpus-progress ID`/`corpus-status ID`. Catalog-level metadata/directory problems or malformed artifact filenames fail the command; no automatic reset or repair occurs. Arbitrary error/source text is omitted from catalog output. A corrupt payload can still have valid metadata, so selectable does not mean audited.

Discovery enumerates/sorts all matching filenames on each page, but only opens metadata for that page (and each selected plan's inventory manifest). Whole selected JSON manifests/plans remain in memory. Thus output and metadata-read counts are window-bounded, not total directory work or per-file bytes. No source object reads, GitHub requests, acquisition locks or automatic retries occur.

## PR comparison evidence and limits

Selected-item acquisition preserves the [PR endpoint's diff response](https://docs.github.com/en/rest/pulls/pulls#get-a-pull-request) as UTF-8 in a `.diff` object. Bulk backlog acquisition instead creates the diff from the fetched base and head commits. The recorded revisions and final metadata recheck bind either observation to commits; this is still an observation interval, not an atomic GitHub snapshot.

The initial diff verifier supports ordinary textual hunks and simple unquoted paths. It checks every file/rename header against the complete file list, matches the supplied patches, checks hunk lengths, and reconciles additions/deletions with both file and PR metadata. Binary changes, submodules, unusual/quoted paths, metadata-only changes, missing patches, truncated hunks, and mismatches stay partial with explanations. A verified empty diff requires a verified empty file list and zero change counts. Completeness means supported textual coverage, not semantic equivalence, surrounding-code review, or executed tests.

Checks are scoped to the recorded PR **head SHA**, not a branch name or synthetic merge commit. The collector verifies the base and available head repository identities, then separately paginates [check suites](https://docs.github.com/en/rest/checks/suites#list-check-suites-for-a-git-reference), each suite's [runs with `filter=all`](https://docs.github.com/en/rest/checks/runs#list-check-runs-in-a-check-suite), and [commit statuses](https://docs.github.com/en/rest/commits/statuses#list-commit-statuses-for-a-reference). Suite-by-suite traversal avoids the aggregate endpoint's 1,000-suite limit. Each array entry records its kind, repository, SHA, endpoint, observation time, and raw response. Status history is retained without flattening it into a single result. Suite/run head mismatches, missing forks, unavailable endpoints, and count drift make coverage partial.

Pending, failed, skipped, neutral, and unknown outcomes remain source values. Successfully fetched empty collections mean no visible results, **not passing checks**. Required-check policy, annotations, logs, test coverage, synthetic merge/merge-queue checks, and deleted historical runs are not collected or inferred. Counts include typed suite/run/status records, not a count of tests.

Closing relationships come from the fixed `ClosingIssues` GraphQL query over [`closingIssuesReferences`](https://docs.github.com/en/graphql/reference/pulls#pullrequest), including the default manually linked relationships. Query text is a code constant; IDs/cursors travel as JSON variables. Every page verifies the PR/repository node IDs, URL, and revision, then retains issue/repository node IDs, full names, number, URL, state, and update time. Cross-repository numbers never collapse into the target repository's namespace. No body-keyword parsing invents relationships, and a link means an issue **may** close, not that closure is guaranteed. Partial GraphQL errors, nulls, missing nodes, repeated cursors, or count drift remain explicit gaps. Coverage is limited to records visible to the authenticated reader, not inaccessible or deleted history. [GraphQL queries use POST but do not mutate state.](https://docs.github.com/en/graphql/guides/forming-calls-with-graphql)

## Identity and compatibility

Repository records contain `host`, `full_name` (`owner/repo`), `database_id`, and `node_id`. Item identities contain `kind`, `number`, `database_id`, and `node_id`, within that repository scope. Unknown IDs are `null`, never fabricated.

The cache host defaults explicitly to `github.com`. `bin/cache --host enterprise.example fetch ...` explicitly targets that host, not the active `gh` account. One owner/repo cache directory belongs to one host; a conflicting host is refused. This does **not** add enterprise-host support to legacy inventory/enrichment commands.

Acquisition reads repository identity from the intended host and calls `EvidenceCache.bind_repository` before publishing evidence. Binding pins stable IDs and rejects contradictory or missing previously pinned IDs. Changed names, including case aliases, require explicit reconciliation, never silent cache movement. Item acquisition verifies the returned number, URL, kind, stable IDs, and PR base repository; subsequent observations reject conflicting item IDs. Automatic alias reconciliation and legacy-ledger identity migration remain unimplemented.

New artifacts require `schema_version: 1` and a recognized `artifact` discriminator. Unsupported versions, unexpected metadata fields, and invalid references fail closed. The executable validators live in `bin/_evidence.py`. Cache initialization does not rewrite ledger rows, CSVs, or group records. IDs and hashes are not authentication, and cached text is never configuration, executable instructions, or approval authority.

## Snapshot contract

An `evidence-cache` record contains the repository identity. An `evidence-snapshot` manifest contains:

| Field                        | Meaning                                                                                                         |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `snapshot_id`                | SHA-256 of canonical sorted-key compact UTF-8 JSON, excluding this field                                        |
| `repository`                 | Explicit host/name and available stable IDs                                                                     |
| `started_at`, `completed_at` | Timezone-aware acquisition interval, not a globally consistent GitHub snapshot                                  |
| `requested_components`       | Distinct requested components; unlisted components were not requested                                           |
| `items`                      | Nonempty selected scope; each entry has `identity`, observed `revision`, and exactly the requested `components` |

Components are `summary`, `comments`, `files`, `diff`, `reviews`, `review_comments`, `checks`, `closing_issues`, and `timeline`. Discussion, submitted reviews, and inline review comments are separate. The selected-item profiles above acquire these components; `closure-watch` supplies timeline events.

Item and component revisions contain `updated_at`, `base_sha`, and `head_sha`; unknown values are `null`. Present SHAs must be full commit hashes. Complete files/diffs require both commits; checks require a head. Component revisions may differ from the item's latest observation: completeness does not imply revision consistency.

Each component contains:

- `status`: `complete`, `partial`, `unavailable`, `failed`, or `not_applicable`.
- `fetched_at` and `source`: observation time and `{transport, resource}` provenance. Transport is `rest`, `graphql`, `gh`, or `git`; resource describes the endpoint/query/operation, never executable input. Do not include credentials or authorization headers.
- `revision`: what was observed during that component's acquisition.
- `expected_count`, `received_count`, `pagination_complete`, and `truncated`: coverage; unknown counts/pagination are `null`.
- `error`: nonempty explanation for incomplete evidence, otherwise `null`.
- `object`: `{sha256, bytes, format}`, or `null` when no payload was obtained. Formats are `json`, `text`, or `diff`; current structured components require JSON and raw diffs require `diff`.

Collection payloads are JSON arrays matching `received_count`. Complete collections require finished pagination, no truncation, and agreement with known expected counts. Verified empty arrays differ from missing evidence. Fetchers must recognize server caps and omitted content; a successful request alone is not proof of completeness. Partial payloads may be retained with explicit partial status.

Summary JSON objects require `state`: `open`, `closed`, `merged`, `unknown`, `unavailable`, `deleted`, or `transferred`. Only PRs can be merged. Fetchers must substantiate the observation; unavailable is not equivalent to closed. This does not alter legacy ledger state semantics.

`not_applicable` is restricted to PR-only components on issues, without fabricated provenance or payloads. It cannot stand in for a missing PR diff.

## Coverage and freshness

A manifest's existence means publication finished, not that every request succeeded. Coverage is derived across requested components and selected items. Partial manifests explicitly report gaps and never grant approval or establish duplication.

`component_problems` checks freshness separately against an explicit revision, time, and maximum age. Files/diffs compare base/head; checks compare head; other components compare `updated_at`. All components also have an age window, since upstream changes do not always move an item's update timestamp. Unknown revisions, changed commits, and expired observations are reported. The reader uses the latest recorded item revision; an explicit refresh obtains a new remote observation.

Reused objects retain their original observation times, even if earlier than the new snapshot's start. An unchanged diff need not be copied when comments refresh. Rebuilding the current-item index cannot retroactively change immutable snapshots.

## Publication and recovery

```text
data/<owner>/<repo>/cache/
  .gitignore                  ignores this entire cache directory
  cache.json                  version and repository binding
  objects/<sha256>.json|diff   immutable, directly searchable UTF-8 payloads
  snapshots/<sha256>.json     immutable manifests referencing objects
  indexes/current.json       checksummed, rebuildable latest-item pointers
  indexes/current.pending    interrupted-publication invalidation marker
  jobs/<kind>-<n>-<part>.json  disposable validated REST page checkpoints
  jobs/cooldown.json          persisted acquisition retry deadline
  local/                      locks and interrupted temporary files
```

`EvidenceCache.publish(manifest, payloads)` validates supplied data, locks the cache, writes and flushes missing objects, and atomically publishes the manifest after all its objects. The derived index is invalidated before the manifest commit and updated afterward, as described above. Existing objects are verified and reused, never silently overwritten. Directory entries are flushed before publishing references. Repeating the same publication is idempotent.

An interruption before the manifest leaves unreferenced objects/temporary files, not a listed snapshot. Retry can reuse verified objects. An interruption after replacement may leave a complete manifest: inspect before retrying. Readers reject corrupt/missing objects, bad manifest hashes, and symlinked cache artifacts. References contain validated hashes/formats, not caller-controlled paths.

Selected acquisition serializes runs and job inspection with one local acquisition lock, separate from cache publication/read/rebuild locks and ledger transactions. A full offline rebuild holds the cache lock throughout verification and can delay other cache users. An ignored cache snapshot must not be the only retained evidence for a consequential action.

## Upgrade and backup

From the tool checkout, `bin/install-to /path/to/repository --dry-run` previews the new managed cache/local ignore block. Rerun without `--dry-run` when ready. Text outside managed blocks, custom prompts, taxonomy, and ledger data are preserved. Repeated upgrades do not duplicate the block. Malformed markers and unsupported future install versions are refused before changes. Ignore rules do not untrack files Git already tracks; inspect existing tracked cache content separately.

Cache initialization also creates its own `.gitignore`, protecting older installs without rewriting their custom rules. Unrecognized nonempty cache directories and custom ignore files during initialization are refused rather than adopted. Supported initialized caches remain intact; unsupported versions are not downgraded.

Stop older writers and back up the install's data/custom configuration before upgrading. Git does not back up ignored caches; in solo mode it may cover none of the install. Copy the entire cache directory, not just manifests, with writers stopped. There is no pruning or destructive data migration in this release. Roll back code only with writers stopped and preserve the backup: old code does not understand these contracts.
