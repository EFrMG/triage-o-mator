# Review groups

A group is a named set of issues and PRs with shared context and individual membership notes. Items can belong to several groups, while their existing categories, recommendations, confidence, and human review status remain in the item ledger.

Groups are stored as one JSON file per group under `data/<owner>/<repo>/groups/`. These files are permanent project data: include them in your normal Git review and commit workflow. Exported packets in `data/<owner>/<repo>/exports/` are disposable snapshots that can be regenerated. Creating a group or setting it to `ready` never changes any item's decision or approval.

## From draft to maintainers

Groups are how organized work reaches lead maintainers. The intended flow is:

1. **Draft:** a contributor, or their agent following [`prompts/organize-groups.md`](../prompts/organize-groups.md), gathers the items behind **one decision** ("Close these five copies in favour of #9323?", "Which of these four PRs should land?"). The description states the decision, a recommendation, and the evidence; each member's notes start with its role (`[original]`, `[duplicate of #X]`, `[best candidate]`, ...).
2. **Ready:** a contributor checks the group, ideally after its members' decisions are reviewed, and sets it `ready`. Agents never do this step.
3. **Maintainers:** `bin/report` lists ready groups first, with every member's decision and review state, and flags members still unreviewed. [`prompts/maintainer-brief.md`](../prompts/maintainer-brief.md) summarizes them; `x` / `bin/group export` gives the full packet. When a group is about to be decided on, [`prompts/polish-report.md`](../prompts/polish-report.md) makes its case: each item checked against its current state and the code, then a recommendation with the case **for** and **against** it.
4. **Archived:** once a maintainer has decided, archive the group; it stays at the end of the list for reference. Archiving is the normal end of a group's life; deleting one is for the mistakes, the group created twice or aimed at the wrong decision.

## TUI workflow

1. Open **Groups** from the sidebar (or press `b` anywhere), then `n` to create one. Groups show as cards beside the sidebar, each with its status, how many members are triaged and reviewed, and its assignee. Fill in a title, description, and assignee. `Tab` / `Shift-Tab` moves between fields, `Enter` confirms a field and moves to the next, saving on the last (`Ctrl-S` saves from anywhere); `Esc` cancels the editor.
2. Open an issue or PR, or tick several in a list with `Space`, then press `b`. Select a group with `j/k` and press `b` again to add the item(s) (`b` always means "put into a group"); the note you enter goes on each of them. Enter a note explaining the relationship, supporting evidence, or suggested review order, then press `Enter` to confirm. An item already in that group has its notes updated instead of being added twice.
3. Press `Enter` on a group to browse its members. The description, contributor attribution, and selected member's notes appear above the list; `Ctrl-D/U` scrolls that context. Each member shows as a card like the item lists', with its current categorization, human review state, and `[agent]` / `[human]` mark. `Enter` opens the full item; `Esc` / `h` returns to the group.
4. `e` edits the selection: inside a group, the selected member's notes; in the group list, the group's metadata, including its status (`draft`, `ready`, or `archived`, changed with `j`/`k`, or `l` for the list; it comes before the assignee). `Space` ticks members, and `d` then `d` again removes the ticked ones (or the selected one). Archived groups remain available at the end of the group list.
   In the group list, `d` then `d` again deletes the selected group itself: the group file and the notes written into it go, and the decisions its members collected stay in the ledger. Since `data/` is git-tracked, a deletion you regret comes back out of Git history. Prefer archiving for a group that was decided; delete the ones that should never have existed.
5. Press `x` to export a Markdown packet with current ledger decisions, notes, attribution, and cross-reference links. Press `X` to also fetch bodies, all returned comments, and PR diffs from GitHub; the status line counts the members as they're fetched ("fetching 3/12: pr #123"), as `bin/group export --enrich` does on stderr. The status line will show the output path.
6. Groups reload from disk every time you open them and after every change, so other contributors' group changes (pulled through Git) show up then. If saving fails because the group revision changed, cancel the editor, close and reopen Groups, then reapply your edit against the new version.
7. On an item, `m` opens the Duplicates screen; its `b` creates a group from the item and the candidates you ticked, with their title similarity in the notes (see the [TUI tutorial, Section 7](tutorial.md)).
8. To triage a group's untriaged members together, open **Batches** from the sidebar, press `n`, and pick the group in the Group field. This is the TUI equivalent of `bin/batch --group`.

The TUI remembers the last selected group for the current session by its stable ID. The item view displays its title. Press `B` to quickly add the current issue or PR (or, in a list, the ticked or hovered items) to that group without opening the browser or notes form. Quick-add re-reads the current group first and preserves an existing membership and its notes. New memberships this way have empty notes.

When editing the decision's reason, printable keys go to that field; press `Tab` or `Esc` to leave it before using `b`, `B`, or other item shortcuts.

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

## Data and collaboration

Each group records a schema version, stable UUID, repository, title, description, assignee, status, revision, creation/update identities and timestamps, and a membership list keyed by `(kind, number)`. Memberships include notes and contributor timestamps. Identities are attributed text, not authenticated identities. Use your signed Git commits to attest contributions instead.

`bin/group` validates references against the current ledger when adding members, rejects blank titles and invalid statuses, serializes local writes with a file lock, and atomically replaces group files. The TUI supplies the revision it loaded on every mutation; the script rejects a stale revision. CLI callers can use `--revision` for the same optimistic check, or omit it to operate on the latest version under the lock.

The TUI never writes group JSON or the item ledger directly: it calls `bin/group` for groups and `bin/apply` for decisions. `data/<owner>/<repo>/ledger.jsonl` stays the source of truth for triage. Group records reference that ledger, and `fetch`, `sync`, CSV round-trips, and decision saves do not erase group memberships.

To hand work to another contributor, share the relevant `data/<owner>/<repo>/groups/*.json` files through Git and/or send an exported review packet. Group files live in their repo's folder and also record the repo, so a checkout pointed at another repository neither displays nor edits them. Group records do not replace the ledger or import exported decisions automatically.

This provides persistent group handoffs, assignment, and conflict detection within one checkout. It does **not** provide a shared server, authenticated access control, distributed locking between Git clones, or concurrent item-ledger writes. Separate contributors should reconcile group-file conflicts through Git review; coordinate ledger changes as before.
