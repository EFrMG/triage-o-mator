# Tutorial: the triage-o-mator TUI

A tour of the TUI. Follow the sections in order; each takes a minute or two. The [README](../README.md) explains what the project is, and [keybindings.md](keybindings.md) lists every key.

Install it into the repository you want to triage ([install.md](install.md)), then launch it from there:

```sh
cd /path/to/your/repository
./triage-o-mator/bin/triage-o-mator
```

Saves, approvals and group edits change your repository's git-tracked ledger and groups for real. To practice, work on a throwaway branch (`git switch -c tutorial`) and drop it afterwards.

## 1. Start from the overview

- The overview shows triage and review progress and suggestions from `bin/next`, marked `[agent]` or `[human]` according to who should do them.
- `j`/`k` move, `Enter` opens, and `Esc` goes back. The breadcrumb in the top border shows where you are.
- The footer lists the keys available on the current screen; `?` explains them.
- `t` opens the theme picker, where `/` searches the theme names.
- `q` quits and warns before discarding unsaved decisions. `Ctrl-C` forces the TUI to exit.

Terminals smaller than 60×24 show a resize prompt.

## 2. Refresh the backlog

- `r` fetches changed issues and PRs from GitHub and syncs them into the ledger. The app also checks tracked comments during this normal refresh.
- `R` performs a full fetch. An incremental fetch cannot detect an issue or PR that was deleted or transferred; the next full fetch, run automatically at least daily or manually with `R`, marks items missing from the open list closed.
- Refreshes are read-only against GitHub and preserve local decisions and review history.

## 3. Choose what to work on

- The sidebar has queues for untriaged items, pending review, merge-ready PRs, close candidates, and all items. Batches, groups, possible duplicates, notifications, and repository switching follow them.
- Open **Untriaged**, then press `i` to cycle between issues, PRs, and both, or `O` to reverse the order between oldest and newest. The title and breadcrumb show both active choices.
- Open **Batches** instead when you want to work through a prepared selection. Use `j`/`k` and `Enter` to select an item in either list.
- `/` searches an item list by title words or `#number`.
- `Space` ticks an item and advances the cursor. An item action then applies to everything ticked, or to the item under the cursor when nothing is ticked. Bulk and destructive actions ask for the same key a second time, except dismissing one notification with `d`.

## 4. Read the complete item

- Open any item with `Enter`.
- Its tabs—Body, Agent notes when present, Comments, and Diff for PRs—run across the top. `H`/`L` switch between them, `1`–`4` jump to one, and `Enter` shows one full screen.
- `o` opens the item on GitHub.

The author below the title is blue; open and closed states are green and red. Each comment shows its creation date at the bottom right in UTC when that date is available. Older saved comments without dates remain undated.

Read comments as well as the body: workarounds, links to the real duplicate, and evidence that a problem remains current often live there.

## 5. Record a decision

- In an untriaged item, `l` moves into the form: Category, Action, Confidence, and Reason.
- On a choice, `j`/`k` change the value and `Enter` moves to the next field. Category, action, and confidence come from `config/taxonomy.json`; reason is free text.
- `s` saves through `bin/apply`, attributed to your `git config user.name`. An unchanged placeholder decision or an empty reason needs a second `s`. After saving, you return to the list on the next item.
- `S` saves and approves when you are ready to confirm the decision yourself. It records your decision and human review together and returns to the list. Untouched defaults or an empty reason need a second `S`; this shortcut has no GitHub side effects.
- While typing the reason, `Enter` saves and approves. `s` and `S` are ordinary letters there; to save for later review instead, press `Tab` then `s`. `Ctrl-S` remains an optional save-for-review alias while typing.
- Unsaved edits are kept as drafts while you look at other items, marked `unsaved` in yellow in the lists; `q` warns before discarding them.

## 6. Let an agent prepare a batch

- On any screen, `y` copies what is in front of you as Markdown: the item you have open, the ones you ticked, or the one under the cursor. `Y` copies the whole screen's worth—the list, batch, or group. If no clipboard tool is available, the status line shows the export file it wrote instead.
- Paste that context into your agent's chat and ask it to investigate, or ask it to "triage 10 items but don't apply them, I'll check them in the TUI." The copied block names the repository, install, and commands needed to read more.
- Open the result under **Batches**. Each item is prefilled with the agent's proposal and notes.
- `s` saves a proposal for review, including any changes you make. `S` saves and approves a proposal you have checked. An unchanged proposal retains its original author, with you recorded as reviewer; a changed proposal becomes your revised decision. Agent notes are preserved either way.
- `A` twice applies every remaining proposal as an unreviewed agent decision. Already-triaged items are kept, and the applied proposals then appear in **Pending Review**.
- `n` in **Batches** makes a new batch; `Enter` on its last field creates it.
- `d` twice deletes a finished batch. Decisions already recorded in the ledger remain; unapplied proposals are lost. Batch files are ignored working copies, so Git cannot restore them.

## 7. Review proposed decisions

Human review distinguishes an agent's proposal from a decision you stand behind. Check the recommendation, its evidence, and any uncertainty before approving it. You can save and approve your own decision with `S`; a second reviewer is not required.

A saved decision is a proposed call until a human marks it reviewed. Review records the confirmation in the ledger; it does not label, comment on, close, approve, or merge anything on GitHub. Reviewed decisions leave **Pending Review**, remain available in **All Items** and their groups, and appear in `bin/report` under **Human-reviewed, ready to act**.

- In **Pending Review**, `a` marks the saved decision in front of you reviewed. The TUI calls this approval, but it is approval of the ledger decision, not approval of a pull request or execution of its recommended action.
- If the form has unsaved edits, `a` warns that it will approve the saved decision. Use `S` to save and approve the edited call together, or `s` to save it for later review. Editing a reviewed decision later removes its review because the confirmation applied to the old call.
- `u` undoes one step: first the approval, then the decision. Right after `a`, it acts on the item you just approved, even though it has left **Pending Review**.
- `Space` ticks items in any list, so `a`, `u`, and other actions work on all of them at once.

## 8. Resolve likely duplicates

- Candidate scores come from title similarity in the local ledger. They are prompts to compare items, not evidence by themselves, and differently worded duplicates may not appear.
- On an item, `m` compares it with lookalike items. That item is the top card, the original the others are judged against: `k` reaches it and `Enter` reads it, so you can compare without leaving.
- On a candidate, `d` `d` rules it out as a duplicate of the item on top; `m` marks that candidate a duplicate of the item on top, with its form prefilled. If it is the older item, the status says so. `M` swaps the two to close the top item instead. `s` saves from the item or comparison, and you stay there to work through the rest.
- **Possible Duplicates** lists same-kind lookalike pairs across the repository, strongest first and older item first. `Enter` or `m` compares one. Resolved pairs are marked `handled` until you reopen the list; `D` `D` clears them from view. `d` `d` records a pair as checked and not duplicate, so it is not offered again until someone withdraws that record.
- `Space` ticks candidates and `b` creates a group containing the item and those candidates, with their similarity in the member notes.
- For a body- and comment-aware comparison, ask an agent "is #N a duplicate?"; it follows [`prompts/find-duplicates.md`](../prompts/find-duplicates.md).

## 9. Organize related work into groups

A group collects related items so maintainers can decide on them together ([groups.md](groups.md)).

- In **Groups**, `n` creates one.
- On any item, `b` adds it to a group with a note; `B` adds it to the last group used.
- `e` edits a group, including its status: `draft`, `ready` for maintainers, or `archived`.
- A ready group does not approve its members. Their individual review states remain visible in reports and exports.

## 10. Build deeper offline context when needed

- Press `f` from the overview, a list, or an item to open **Local dataset**. Opening it makes no GitHub request.
- `d` freezes the open backlog and begins downloading its supported evidence. `n` chooses an item limit, `r` resumes the current download, `u` measures cache size, and `y` copies the agent prompt.
- Saved checkpoint counts refresh while downloading. `x` cancels the operation; `Esc` closes **Local dataset** without stopping it.

Counts describe saved outcomes, not complete or current coverage. Custom corpus scopes and profiles remain available through the cache CLI; see [corpus preparation](evidence-reference.md#corpus-controls-in-the-tui). Dataset work changes no sync, decision, or approval state.

## 11. Track follow-up activity

- Press `w` on an issue or PR in a list or item view to track its comments. The app checks tracked items at startup and during a normal refresh with `r`.
- **Notifications** places new tracked comments and unviewed saved PR activity or imported actions under **Needs attention**. Quiet or viewed tracked items and viewed saved records appear under **Past actions**.
- Select with `j`/`k` or `Tab`. `v` marks an alert viewed; one `d` dismisses it. Dismissing a tracked item stops its comment checks. Dismissing retained watch or imported-action activity hides the row without deleting its evidence.
- `Enter`, `l`, or `→` opens a tracked item with a fresh read from GitHub. For retained PR activity, it first opens cards containing bounded excerpts; the same keys open a card's PR, while `Esc` or `h` returns to the selected card. Previous and More cards page within that screen.

Opening the notification list and retained cards makes no GitHub request. Reading does not mark activity viewed, resolve an appeal, or approve a decision. A refreshed PR and its retained card can describe different moments.

After explicitly enrolling and polling a closed-PR watch through [the watch commands](appeal-evidence.md#explicit-closed-pr-watches), its saved activity also appears here. Check the original rationale, observed state, source gaps, and last successful check together through the [offline CLI](appeal-evidence.md#needs-attention-bounded-offline-readers) when a full reassessment is needed. Activity is a review lead, not a confirmed appeal.

For a requested reassessment from imported closure history through explicit enrollment and source review, follow [review-appeal](../prompts/review-appeal.md). No replacement PR is required; the workflow reports a local reassessment and leaves GitHub actions to separately authorized work.

## 12. Hand reviewed work to maintainers

- From a group, `x` exports a Markdown review packet with current decisions and notes. `X` also fetches bodies, comments, and PR diffs; the status line shows its progress and output path.
- `bin/report` gathers ready groups and individual human-reviewed decisions. An agent following [`prompts/maintainer-brief.md`](../prompts/maintainer-brief.md) can turn that report into a concise maintainer brief.
- Exporting or reporting does not act on GitHub. Labeling, approving, and merging remain separate work.
- On an item, `c` composes a conversation comment inline, while `C` opens `$EDITOR`. `Ctrl-P` toggles a rendered Markdown preview; `Ctrl-S` approves and publishes the exact target and text. In preview, `C` reopens `$EDITOR`; `Esc` returns to editing. From the inline editor, `Esc` discards the draft.
- On an open item, `x` composes an explanatory closing comment inline and `X` uses `$EDITOR`. `Ctrl-S` approves the comment and closure together. After confirmation, the TUI updates the visible state and refreshes the item discussion and ledger.
- On a closed item, `v` composes a reopening comment inline and `V` uses `$EDITOR`. These keys also work in item lists: select closed items with `Space`, then use one shared comment for all selected items; without a selection, they use the hovered item. For multiple items, the first `Ctrl-S` opens a scrollable review of every target and the comment, and the second approves them. The TUI stops if an outcome is uncertain and reports how many were confirmed open.

## 13. Move between repositories without mixing their work

- **Switch Repo** lists repositories in this install and in every other install recorded on the machine. Each keeps its own taxonomy, ledger, batches, groups, exports, and reports.
- Typing filters the list. `Tab` moves to the results, `j`/`k` selects one, and `Enter` opens it. An absolute path opens an install directly; an unlisted `owner/repo` starts that repository in the current install with a full fetch.
- A path without an install opens the plan from `bin/install-to --dry-run`. Read it, press `Enter` again to install exactly that plan, or `Esc` to leave the repository untouched. See [install.md](install.md#installing-from-inside-the-app).
- The TUI waits for saves and fetches to finish before switching and asks you to save or discard drafts.
- When something fails, the status line says why, and `!` shows the command and its full output.
