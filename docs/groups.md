# Review groups

A group is a named set of issues and PRs with shared context and individual membership notes. Items can belong to several groups, while their existing categories, recommendations, confidence, and human review status remain in the item ledger.

Groups are stored as one JSON file per group under `data/<owner>/<repo>/groups/`. These files are permanent project data: include them in your normal Git review and commit workflow. Exported packets in `data/<owner>/<repo>/exports/` are disposable snapshots that can be regenerated. Creating a group or setting it to `ready` never changes any item's decision or approval.

## From draft to maintainers

Groups are how organized work reaches lead maintainers. The intended flow is:

1. **Draft:** a contributor, or their agent following [`prompts/organize-groups.md`](../prompts/organize-groups.md), gathers the items behind **one decision** ("Close these five copies in favour of #9323?", "Which of these four PRs should land?"). The description states the decision, a recommendation, and the evidence; each member's notes start with its role (`[original]`, `[duplicate of #X]`, `[best candidate]`, ...).
2. **Ready:** a contributor checks the group, ideally after its members' decisions are reviewed, and sets it `ready`. Agents never do this step.
3. **Maintainers:** `bin/report` lists ready groups first, with every member's decision and review state, and flags members still unreviewed. [`prompts/maintainer-brief.md`](../prompts/maintainer-brief.md) summarizes them; `bin/group export` gives the full packet. When a group is about to be decided on, [`prompts/polish-report.md`](../prompts/polish-report.md) makes its case: each item checked against its current state and the code, then a recommendation with the case **for** and **against** it.
4. **Archived:** once a maintainer has decided, archive the group; it stays at the end of the list for reference. Archiving is the normal end of a group's life; deleting one is for the mistakes, the group created twice or aimed at the wrong decision.

## TUI behavior

The [tutorial](tutorial.md#9-organize-related-work-into-groups) covers group controls, including adding items, editing and exporting. Groups show status, assignee, member triage and review counts, notes, and attribution. The TUI reloads groups when opened and after changes; a stale revision rejects a save so the contributor can reopen the group and reapply the edit.

Deleting a group removes its file and membership notes, while item decisions stay in the ledger. Archive groups that were decided; reserve deletion for mistaken groups. Quick-add uses the last selected group, re-reads it, and preserves existing notes; new memberships have empty notes. A batch can select that group's untriaged open members through `bin/batch --group`.

## Script workflow

All mutations require an explicit contributor identity, except `delete`, which records nothing to attribute. Commands return JSON except Markdown exports. Copy the stable `id` from `create` or `list` for subsequent commands.

```sh
bin/group create --title 'Session startup regressions' \
  --description 'Related reports and proposed fixes; verify the shared cause.' \
  --assignee 'Maintainer name' --by 'Contributor name'

bin/group list
bin/group show GROUP_ID
bin/group add GROUP_ID --kind issue --number 123 \
  --notes 'Primary reproducer; compare with PR #456.' --by 'Contributor name'
bin/group add GROUP_ID --kind pr --number 456 \
  --notes 'Candidate fix for issue #123.' --by 'Contributor name'
bin/group update GROUP_ID --status ready --revision 3 --by 'Contributor name'
bin/group remove GROUP_ID --kind issue --number 123 --by 'Contributor name'

# Deleting the group itself takes no --by: nothing is left to attribute it to. The deleted group is printed as {"deleted": {...}}, the run's only record of what was there.
bin/group delete GROUP_ID --revision 4

bin/batch 25 --group GROUP_ID
bin/report --stdout

bin/group export GROUP_ID --output data/<owner>/<repo>/exports/review.md
bin/group export GROUP_ID --diff --format json --output data/<owner>/<repo>/exports/review.json
```

`bin/batch --group` selects only that group's untriaged open items, respecting the existing size, kind, and ordering filters. The enriched context includes the group title, description, assignee, status, revision, and membership notes; decision templates and `bin/apply` remain unchanged. `bin/report` includes group summaries.

`export --enrich` fetches bodies and comments; `--diff` implies enrichment and adds PR diffs. These remain read-only GitHub calls. Without those flags, exports work offline and use current ledger facts. Missing ledger references are explicitly marked, not silently dropped. A packet contains all members, including closed and reviewed items.

Each exported item also carries its local context revision, projected ledger guidance and IDs of its relevant non-archived groups. The selected group's notes remain in `group`; any other relevant groups appear once in `related_groups`, with their revisions, membership notes and attribution. Markdown shows the revisions and other group guidance without repeating shared notes for every member. This context uses local records only, including when explicit enrichment is requested; a saved packet does not claim that later edits or GitHub activity were included.

PR members also carry a separate `feedback_checkpoint` and retained `feedback` entries for attributed proposal rejections, recorded comment/close outcomes and a pending proposal with a saved write receipt that still needs reconciliation. The Markdown packet shows these after local notes. Long reasons are available through the bounded `bin/item-context source` reader; viewing or dismissing a proposal is local presentation and is not included. A successful recorded write is not proof that the proposal was correct, and an uncertain outcome still needs reconciliation.

Add `--cache-mode offline|cache-preferred|refresh` to enrichment to use the [shared evidence reader](evidence-reference.md#similarity-and-group-consumers). For example, `bin/group export GROUP_ID --diff --cache-mode offline --format json --output data/<owner>/<repo>/exports/review.json` requires no GitHub calls. Available text and per-item snapshot references are fixed in the export; missing/partial/stale evidence is reported, not filled with live reads. `--snapshot ID` requires offline mode and every member in that snapshot. Markdown exposes coverage gaps as well as group/decision context; JSON also carries full component references. A successfully written file does not imply complete evidence or approval.

`--output` is atomically replaced only after acquisition/rendering finishes, so an interrupted export cannot expose a partly written new packet. See [export recovery](storage.md#group-exports). Cached group exports currently use the CLI.

## Data and collaboration

Each group records a schema version, stable UUID, repository, title, description, assignee, status, revision, creation/update identities and timestamps, and a membership list keyed by `(kind, number)`. Memberships include notes and contributor timestamps. Identities are attributed text, not authenticated identities. Use your signed Git commits to attest contributions instead.

`bin/group` validates references against the current ledger when adding members, rejects blank titles and invalid statuses, serializes local writes with a file lock, and atomically replaces group files. The TUI supplies the revision it loaded on every mutation; the script rejects a stale revision. CLI callers can use `--revision` for the same optimistic check, or omit it to operate on the latest version under the lock.

The TUI never writes group JSON or the item ledger directly: it calls `bin/group` for groups and `bin/apply` for decisions. `data/<owner>/<repo>/ledger.jsonl` stays the source of truth for triage. Group records reference that ledger, and `fetch`, `sync`, CSV round-trips, and decision saves do not erase group memberships.

To hand work to another contributor, share the relevant `data/<owner>/<repo>/groups/*.json` files through Git and/or send an exported review packet. Group files live in their repo's folder and also record the repo, so a checkout pointed at another repository neither displays nor edits them. Group records do not replace the ledger or import exported decisions automatically.

This provides persistent group handoffs, assignment, and conflict detection within one checkout. It does **not** provide a shared server, authenticated access control, or distributed locking between Git clones. Ledger writers in one install serialize updates, and CSV imports also check exported row revisions. Separate contributors should reconcile group-file and ledger conflicts through Git review.

Ordinary edits preserve historical structured fields already present in older group files. New review packets omit those retired fields and focus on the group's current membership, notes, assignment and status.

## Pinned candidate discovery

Generate review-set suggestions from an explicit immutable snapshot or a frozen corpus's pinned progress:

```sh
bin/cache candidates --compact --corpus CORPUS_ID --limit 20 > candidates.json
# Or: bin/cache candidates --snapshot SNAPSHOT_ID --limit 20
bin/group create-candidate --file candidates.json --candidate CANDIDATE_ID --by agent:contributor
```

Use a `candidate-set` ID from the returned page. `--compact` keeps selected proposal IDs and bounds pair previews while summarizing all-scope diagnostics; omit it when the full observations and negative-verdict records are needed. Saving reconstructs and verifies the complete suggestion from pinned evidence and current negative verdicts under the group transaction; altered result text is not trusted. The new group is a draft with generic membership and a checksummed `candidate_origin`. It has no duplicate verdict, survivor decision or approval. Repeating the explicit creation command creates another group; inspect existing groups before saving it again. Creation needs the cache; later group reads/exports retain provenance even if the cache becomes unavailable. Membership edits leave the original proposal intact as historical provenance, not a claim about the edited set. JSON and Markdown exports include that origin.

`pr-candidate-sets-v1` uses these independent discovery signals:

- Shared exact file paths, excluding known lockfile basenames and paths containing `vendor`, `generated`, `node_modules` or `dist`.
- Shared closing-issue identities, including their repository identity, from the pinned `closing_issues` component.
- Title similarity, with `--title-threshold 0.8` by default.

Every pair in a proposed set must share a direct signal; graph connectivity alone is insufficient. `--max-frequency 10` suppresses broad file/link signals. Missing, partial, stale and suppressed evidence remains explicit, and recorded `not-duplicate` verdicts exclude their pairs. These are discovery leads, not duplicate verdicts or survivor choices.

Discovery reads only the selected snapshot or pinned corpus progress and never fetches replacements. Continuations bind the evidence, verdicts and options. The current scope limit is 5,000 members and 256 MiB of declared summary, file and closing-link payloads; larger scopes fail without partial suggestions. No existing group or decision is rewritten.

## Pairs already ruled out

`bin/not-duplicate --key issue:12 --key issue:34 --by "Reviewer" --note "why"` records that a human compared two items and found them distinct, in git-tracked `data/<owner>/<repo>/not-duplicates.jsonl`. `--list` prints the records and `--remove` withdraws one.

`similar` item candidates, `similar --pairs`, `next` and newly created batches all exclude recorded pairs. Filtering happens before the item candidate limit, so a ruled-out match does not displace another lead. Title-pair enumeration examines every qualifying open pair rather than capping each item's neighbors at ten. These are still title leads, not semantic conclusions or a complete duplicate search.

For explicit inspection, `similar --kind K --number N --include-checked` and `similar --pairs --include-checked` retain the ruled-out candidates with their attributed `negative_verdict`. `--query` is free-text topic search with no source pair, so pair verdicts never remove its results. Filtered-out candidates are not enriched; combining `--include-checked` with enrichment explicitly includes the ruled-out items.

New TUI item suggestions inherit script filtering. An already-open comparison can retain a ruled-out card for inspection, marked as such; its cached item header no longer advertises that pair.
