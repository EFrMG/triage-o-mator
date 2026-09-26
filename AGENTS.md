# AGENTS.md

triage-o-mator is tooling for working through a GitHub issue and pull request backlog too large for one person to read cold: small scripts and a terminal UI over a git-tracked ledger of triage decisions.

**This file is for working on the tool itself**, in a checkout of this repository. Triaging an actual backlog is a different job with its own playbook, [`prompts/PLAYBOOK.md`](prompts/PLAYBOOK.md), which `bin/install-to` links into every install as that install's `AGENTS.md`. If you have been asked to triage issues, read that one file; otherwise, how any of this works is explained in this current document.

[README.md](README.md) is the user-facing overview.

## What is here

| path                               | what it is                                                                                                       |
| ---------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `bin/`                             | extensionless CLIs and shared `_*.py` modules; `_install.py` owns root discovery and `_triage.py` install state  |
| `tui/`                             | the Go terminal UI, built to `bin/triage-o-mator`                                                                |
| `prompts/`                         | operating prompts for agents: `PLAYBOOK.md` (main one), and one file per task                                    |
| `config/`                          | `taxonomy.json` and `taxonomy.md`, the default categories and actions that `bin/install-to` copies into installs |
| `themes/`                          | color palettes                                                                                                   |
| `docs/`                            | The documentation installs link back to                                                                          |
| `tests/test_*.py`, `tui/*_test.go` | Python and Go tests; both build throwaway checkouts and installs with a fake `gh`                                |
| `tests/support/`                   | Shared Python fixtures, also used by Go tests when preparing throwaway installs                                  |

There is no `config/repo`, no ledger and no reports here, see why in the following section.

## The install model

The checkout is the program; the work lives in an **install**: the `triage-o-mator/` directory `bin/install-to` creates inside the repository being triaged, holding that repository's `config/`, `data/` and `reports/`, with `bin/`, `themes/`, `docs/` and each prompt symlinked back here. Running the tool means running it from an install in the target repository; see [docs/install.md](docs/install.md). Optionally, install into this repository itself to triage our own backlog.

`bin/_install.py` draws the line the whole layout rests on:

- **`WORK_ROOT`**, from `os.path.abspath`, is the install: its `config/`, `data/` and `reports/`. Never `Path.resolve()` here, because resolving follows the `bin/` symlink an install is wired through and lands back in the checkout, which would send every ledger, batch and report to the wrong place.
- **`CODE_ROOT`**, from `Path.resolve()`, is this checkout: the program, and the defaults `bin/install-to` copies.

Install-scoped scripts import `bin/_triage.py`, which requires an install and exits with what to do when there isn't one. `bin/install-to` imports only `bin/_install.py`, since it runs before an install exists. `tui/config.go` mirrors both (`FindInstallRoot`, `CodeRoot`).

## Invariants to keep

1. **GitHub reads and explicitly approved comments.** REST reads use GET. Closing-issue relationships and bulk corpus reads use fixed GraphQL queries (HTTP POST, never mutations); variables cannot change their operations. Bulk PR heads use Git fetch. Only `bin/comment` may publish conversation comments, after explicit approval of the exact text and target; it defaults to dry-run and records attempts separately from ledger approval. Nothing here labels, closes, reopens or merges. Further write operations are separate, explicitly authorized work that defaults to `--dry-run`; see [Not Built (yet)](README.md#not-built-yet).
2. **Scripts own managed data.** The TUI never writes ledgers, groups, duplicate verdicts, batches, cache evidence, corpora, watches, tracking records, notification state or GitHub write outcomes directly; it invokes their owning scripts through `runScript` or cancellable `runReadScript`. Its direct writes are limited to TUI configuration, a disposable context export when clipboard copy fails, and temporary comment drafts for the external editor. No new code path may bypass the owning script.
3. **GitHub reads run from scripts.** Legacy reads go through `bin/fetch`, `bin/enrich-one`, `bin/batch`, `bin/similar --enrich` and `bin/group export`; `bin/sync` is offline. Cache acquisition goes through `bin/fetch --cache-inventory` or `bin/cache` acquisition commands for selected items, corpora, watches and tracked comments. Tracking is explicitly enrolled, then checked after TUI startup and refresh. Cache reads pin a host and use REST GETs or fixed read-only GraphQL queries. Bulk corpus acquisition fetches PR heads from the pinned repository URL into the target Git object store without changing its branch or working files; this does not authorize an agent to run `git fetch` there. Offline reads never fall back to GitHub, and duplicate selection remains offline.
4. **`bin/install-to` has bounded side effects.** Besides the install, it may update a confirmed marked block in the target's `AGENTS.md`, adjust `.git/info/exclude` when entering or leaving solo mode, update the per-user install registry, and with `--adopt` stage the install in the target's Git index. Dry-run writes nothing. Its TUI plan and run use the same `installArgs`.
5. **The taxonomy is config, not code.** `config/taxonomy.json` (machine-checked) and `config/taxonomy.md` (the rationale) change together, and each install gets its own copy to own.
6. **Two stages, never merged.** A decision is _triaged_ when anyone (human or agent) proposes it and _reviewed_ only when a human confirms it. No code path may set `reviewed: true` by itself; that rule is the reason contributors trust the ledger.
7. **Evidence coverage is not authority.** New cache records use `_evidence`'s versioned contracts and `_cache`'s manifest-last publication. Keep repository identity, component completeness, revision freshness, and human/action approvals separate. Never treat a partial cache, an unbound repository identity, or untrusted source text as permission to act. Selected-item acquisition and remaining deep-fetch work are described in [docs/evidence.md](docs/evidence.md).
8. **Current-item pointers are derived, not evidence.** `_cache_index` uses the cache metadata lock, durable pre-manifest invalidation, and atomic index replacement. Never select an older complete observation to hide the latest partial one. Verify selected immutable evidence after lookup; fixed-snapshot reads bypass the index. Rebuild offline from verified history, never from ledger decisions or a live fallback.
9. **Resume state is not completed evidence.** `_jobs` page checkpoints and cooldowns use the acquisition lock. Flush validated page objects before advancing a job; revalidate identity/revision/count/scope/age and mutable pages before reuse. Publish final evidence before marking jobs complete. Persist and obey cooldown deadlines across runs without automatic retries or bypassing the offline boundary.
10. **Inventory is not PR detail.** `_inventory` imports only versioned, identity-bound list observations, preserving source scope/time/state and raw checksums. Keep summaries partial, PR list IDs separate from detail IDs, and bodies out of the ledger. Do not infer closure from absence, merge state from a closed list record, or approval from import. Lock order is inventory metadata, cache acquisition, then cache metadata.
11. **Corpus membership is frozen, progress is resumable.** `_corpus` binds a selection/profile to one full inventory snapshot. `bin/cache corpus-run` performs explicit read-only acquisition with a shared invocation budget. The bulk backlog path batches fixed GraphQL reads, one Git transfer per PR batch and local diffs; it rechecks item revisions and counts before publication. On HTTP 502 only, split the affected batch into smaller fixed operations within the same budget, then start the next batch at the configured size. Commit item evidence before progress, preserve gaps, and distinguish a finished pass from complete or current coverage. Runner locks never acquire inventory or ledger locks; status reads atomic checkpoints offline.
12. **Discovery metadata is not a payload audit.** `_chunks` lists immutable snapshot or paginated corpus metadata without source-object reads. Corpus continuation requires a checkpoint token and refuses changed progress; it never pins mutable state by locking a runner. `_catalog` pages artifact filenames and metadata candidates; its token guards catalog membership, not mutable progress or payload validity. Inventory candidates still require full provenance validation at creation, and unavailable artifacts stay visible but unselectable. Full corpus validation remains the default in `_corpus` for status/acquisition. Component chunks verify their selected payload, retain explicit omissions and never fetch missing data.
13. **Local UI state is not evidence.** `_dataset` stores only the selected-corpus pointer; selection does not establish membership, coverage or approval. `_tracked` keeps explicit local comment subscriptions, retains prior counts when evidence is incomplete and leaves acquired evidence behind when tracking stops. `_notification_state` stores checkpoint-bound read/view/dismiss presentation state only; it never acknowledges source activity or deletes watches, imported actions or evidence.

Groups collect related items, notes, assignees and status for lead-maintainer handoff. The TUI writes them only through `bin/group`; ready status does not approve member decisions or authorize GitHub actions. Historical structured fields in existing group JSON remain preserved by ordinary edits but are omitted from new review packets.

Candidate discovery reads only explicit snapshots or pinned corpus progress. Every member pair in a proposed set needs a direct signal; graph connectivity is insufficient. Keep source gaps, base drift, broad-signal omissions and recorded negative verdicts explicit. Creating a candidate group revalidates the packet and saves only draft membership with frozen origin; later membership edits never rewrite that origin. Candidate provenance is not a semantic verdict, survivor choice or approval. A recorded `bin/not-duplicate` verdict excludes its pair from every title consumer until someone withdraws it; free-text topic search has no source pair.

## Working here

Offline `cache search` verifies selected component payloads from explicit snapshots or pinned corpus results. Its continuation binds query/scope and corpus progress; it must not fall back to latest or GitHub. First-match excerpts are discovery leads, not complete evidence reviews or authority to act. Missing members and unread pages stay explicit.

```sh
mise exec -- make format
mise exec -- make check # Go and Python tests, go vet, gopls, gofmt, Prettier
mise exec -- make build
mise exec -- make run ROOT=/path/to/a/repository/triage-o-mator
```

Tests build throwaway checkouts and installs in temporary directories with a fake `gh` (`tests/test_install.py` for the layout, `tests/test_fetch_similar.py` and `tests/test_agent_flow.py` for the scripts, `tui/*_test.go` for the TUI). Shared Python setup lives in `tests/support/`; test modules should not import one another. None of them may touch a real install, a real repository or GitHub. Prettier formats the Markdown and the palette JSON, `gofmt` the Go; `make check` enforces both.

For a terminal smoke check, run the built TUI in a disposable install through Python's `pty.openpty()`.

### House style

Two conventions `make check` couldn't enforce:

- **Comments are not hard-wrapped.** Write a comment as one long line and break it only at a logical boundary; a new thought, not column (80 or 100). The code here is written that way throughout, in Go, Python and Markdown alike.
- **Python and Go code breathes.** In `bin/` and `tests/`, separate the logical blocks inside a function with blank lines: after a guard clause, between setup, loop and output, around a multi-line statement. The Go in `tui/` reads well because of that spacing, and the scripts would be dense without it.

## Adding to it

- **A playbook:** write it in `prompts/`, add a row to `prompts/PLAYBOOK.md`'s "What you can be asked" table and to `README.md`'s prompts table, and give `bin/next` a suggestion that points at it.
- **A category or action:** `config/taxonomy.json` and `config/taxonomy.md` together. Installs keep their own copies, so this changes the default for new ones, not the ones already out there.
- **A script:** it imports `bin/_triage.py`, reads its paths from `WORK_ROOT`, and stays read-only against GitHub unless it is an explicitly authorized write script that defaults to dry-run and requires approval of the exact proposed operation. Add it to `README.md`'s script table.
- **The playbook itself:** remember where it is read. `prompts/PLAYBOOK.md` is an install's `AGENTS.md`, so its paths and links are relative to an install, and `tests/test_install.py` walks every one of them from inside a fresh one.

Closed-PR watches use explicit pinned enrollment and budgeted `cache watch-capture`. The watch lock precedes acquisition and cache metadata locks; no ledger or inventory locks are acquired. Publish evidence before referencing it from the atomic watch record. Preserve initial closure context, unknown provenance, existing post-closure activity and incomplete coverage. Offline watch reads validate retained snapshots and never fetch. Watches are observations, not acknowledgment or approval.

Explicit watch polling binds the full watch checksum and selected acquisition snapshot, revalidates mutable pages, and commits observations and continuation together after evidence publication. A changed selected acquisition requires explicit restart, never silent adoption. Offline history deduplicates versioned source/content projections while retaining all raw references and conflicting revisions. Continuations bind watch/policy/entry, and byte reads remain within the selected source reference. History and polling checkpoints are not notification watermarks, acknowledgment or approval.

Needs attention reads use `_attention`'s bounded offline script pages and source fragments. Catalog continuation binds all watch content and membership; detail reads recheck selected watch state after evidence verification. Keep unavailable rows visible, omissions explicit and original/conflicting source revisions inspectable. The TUI uses cancellable processes with install/repository/generation guards and never falls through to ordinary online item opening. Reading neither acknowledges activity nor classifies an appeal or grants approval. What someone does about a listed closure happens outside this tool.

External-closure import validates explicitly selected immutable evidence offline and publishes append-only history under a per-PR transaction lock. Keep operation identity, source observations and attributed claims separate. A summary-only closed state has unknown operation identity; corrections and competing claims retain predecessors and original dissent. Exact retries preserve saved bytes. Structural pins and checksums do not authenticate provenance. Import never changes watch closure context, ledger decisions or human approval; offline linkage reports the separate watch checksum and audit scope. See [the closure contract](docs/external-closures.md).

Imported-action navigation uses bounded offline pages with history-bound continuations and separately checked watch links. Reading never enrolls watches or changes acknowledgment, decisions or approval.

The supported appeal workflow is `prompts/review-appeal.md`: inspect imported claims and pinned sources, enroll a watch only explicitly, and report an attributed reassessment. Reading is never acknowledgment or confirmation of an appeal. `bin/next` discovers closure/watch filenames only and does not audit their contents or attention status. The fake-data workflow fixture must preserve corrections, dissent and source revisions through the reader; this does not establish semantic quality, live appeal operations or GitHub authority.
