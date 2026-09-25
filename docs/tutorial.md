# Tutorial: the triage-o-mator TUI

A tour of the TUI. Follow the sections in order; each takes a minute or two. The [README](../README.md) explains what the project is, and [keybindings.md](keybindings.md) lists every key.

Install it into the repository you want to triage ([install.md](install.md)), then launch it from there:

```sh
cd /path/to/your/repository
./triage-o-mator/bin/triage-o-mator
```

Saves, approvals and group edits change your repository's git-tracked ledger and groups for real. To practice, work on a throwaway branch (`git switch -c tutorial`) and drop it afterwards.

## 1. Getting around

- The sidebar has queues for untriaged issues and PRs, pending review, merge-ready PRs, close candidates, the oldest untriaged items, and all items. Batches, groups, possible duplicates, and repository switching follow them.
- The overview shows how far triage and review have come, and what `bin/next` recommends doing next.
- `j`/`k` move, `Enter` opens, `Esc` goes back. The breadcrumb in the top border shows where you are.
- The footer lists the keys for the current screen; `?` explains them.
- `t` picks a theme; `/` searches the theme names. `r` fetches the latest changes from GitHub.
- `Space` ticks items and advances the cursor. An item action then applies to everything ticked, or to the item under the cursor when nothing is ticked. Bulk and destructive actions ask for the same key a second time, except dismissing one notification with `d`.

## 2. Reading an item

- Open any item from a list in the sidebar.
- Its tabs (Body, Agent notes when present, Comments, and Diff for PRs) run across the top: `H`/`L` switch between them, `1`–`4` jump to one, and `Enter` shows one full screen.
- `o` opens the item on GitHub.
- In any list, `/` searches by title words or `#number`.

The author below the title is blue; open and closed states are green and red. Each comment shows its creation date at the bottom right in UTC when that date is available. Older saved comments without dates remain undated.

Read comments as well as the body: workarounds, links to the real duplicate, and evidence that a problem remains current often live there.

To prepare a larger frozen selection, press `f` from the overview, list or item, then `d` to download the open backlog. Use `n` to choose an item limit or `r` to resume the current download. Saved checkpoint counts refresh while downloading; `x` cancels, and `Esc` closes Local dataset without stopping acquisition. Counts describe saved outcomes, not complete or current coverage. Custom corpus scopes and profiles remain available through the cache CLI; see [corpus preparation](evidence-reference.md#corpus-controls-in-the-tui). No sync, decision or approval changes here.

## 3. Deciding

- In an untriaged item, `l` moves into the form: Category, Action, Confidence and Reason.
- On a choice, `j`/`k` change the value and `Enter` moves to the next field. Category, action, and confidence come from `config/taxonomy.json`; reason is free text.
- `s` saves through `bin/apply`, attributed to your `git config user.name`. An unchanged placeholder decision or an empty reason needs a second `s`. After saving, you return to the list on the next item.
- `S` saves and approves when you are ready to confirm the decision yourself. It records your decision and human review together and returns to the list. Untouched defaults or an empty reason need a second `S`; this shortcut has no GitHub side effects.
- While typing the reason, `Enter` saves and approves. `s` and `S` are ordinary letters there; to save for later review instead, press `Tab` then `s`. `Ctrl-S` remains an optional save-for-review alias while typing.
- Unsaved edits are kept as drafts while you look at other items, marked `unsaved` in yellow in the lists; `q` warns before discarding them.

## 4. Reviewing

Human review distinguishes an agent's proposal from a decision you stand behind. Check the recommendation, its evidence and any uncertainty before approving it. You can save and approve your own decision with `S`; a second reviewer is not required.

A saved decision is a proposed call until a human marks it reviewed. Review records the confirmation in the ledger; it does not label, comment on, close, approve, or merge anything on GitHub. Reviewed decisions leave **Pending Review**, remain available in **All Items** and their groups, and appear in `bin/report` under **Human-reviewed, ready to act**.

- In **Pending Review**, `a` marks the saved decision in front of you reviewed. The TUI calls this approval, but it is approval of the ledger decision, not approval of a pull request or execution of its recommended action.
- If the form has unsaved edits, `a` warns that it will approve the saved decision. Use `S` to save and approve the edited call together, or `s` to save it for later review. Editing a reviewed decision later removes its review because the confirmation applied to the old call.
- `u` undoes one step: first the approval, then the decision. Right after `a`, it acts on the item you just approved, even though it has left Pending Review.
- `Space` ticks items in any list, so `a`, `u` and other actions work on all of them at once.

## 5. Handing context to an agent

- On any screen, `y` copies what is in front of you as Markdown: the item you have open, the ones you ticked, or the one under the cursor. `Y` copies the whole screen's worth — the list, the batch, the group.
- Paste it into your agent's chat and ask about it ("is this a duplicate?", "which of these are safe to merge?"). The block names the repo, the install, and the commands to read more, so the agent can go deeper on its own.
- If nothing on the machine takes a clipboard, the status line tells you the file it wrote instead.

## 6. Reviewing an agent's batch

- Ask your agent to "triage 10 items but don't apply them, I'll check them in the TUI".
- Open it in **Batches**: each item is prefilled with the agent's proposal and its notes.
- Save a proposal with `s`, changing it first when needed. This records it as your decision and preserves the agent's notes.
- Use `S` to save and approve a proposal you have checked. An unchanged proposal retains its original author (or `agent` when unattributed), with you recorded as reviewer. A changed proposal is recorded as your revised decision. The agent's notes are preserved in either case.
- `A` twice applies every remaining proposal as an unreviewed agent decision. Anything already triaged is kept, and the applied proposals then appear in **Pending Review**.
- `n` in **Batches** makes a new batch; `Enter` on its last field creates it.
- `d` twice deletes a finished batch. Decisions already recorded in the ledger remain; unapplied proposals are lost. Batch files are ignored working copies, so Git cannot restore them.

## 7. Duplicates

- Candidate scores come from title similarity in the local ledger. They are prompts to compare items, not evidence by themselves, and differently worded duplicates may not appear.
- On an item, `m` compares it with lookalike items. That item is the top card, the original the others are judged against: `k` reaches it and `Enter` reads it, so you can compare without leaving.
- On a candidate, `d` `d` rules it out as a duplicate of the item on top; `m` marks that candidate a duplicate of the item on top, with its form prefilled; if it's the older of the two, the status says so. `M` swaps the two, to close the top item instead. `s` saves, from the item or from the comparison, and you stay on the comparison to work through the rest.
- **Possible Duplicates** in the sidebar lists same-kind lookalike pairs across the repo, strongest first and older item first. `Enter` or `m` compares one. Pairs you resolve are marked `handled` until you open the list again; `D` `D` clears them from view. `d` `d` rules a pair out as "checked, not duplicates", which is kept in `not-duplicates.jsonl`, so nobody is offered that pair again until someone withdraws the record.
- `Space` ticks candidates and `b` creates a group containing the item and those candidates, with their similarity in the member notes.
- For a body- and comment-aware comparison, ask an agent "is #N a duplicate?"; it follows [`prompts/find-duplicates.md`](../prompts/find-duplicates.md).

## 8. Groups

A group collects related items so maintainers can decide on them together ([groups.md](groups.md)).

- In **Groups**, `n` creates one.
- On any item, `b` adds it to a group with a note; `B` adds it to the last group used.
- `e` edits a group, including its status: draft, ready (for maintainers) or archived.
- `x` exports a group as a review packet.
- A ready group does not approve its members. Their individual review states remain visible in reports and exports.

## 9. Other repos and failures

- **Switch Repo** lists repos in this install and in every other install recorded on the machine. Each keeps its own taxonomy, ledger, batches, groups, exports, and reports.
- Typing filters the list. An absolute path opens an install directly; an `owner/repo` that is not listed starts that repo in the current install with a full fetch.
- A path without an install opens the plan from `bin/install-to --dry-run`. Read it, press `Enter` again to install exactly that plan, or `Esc` to leave the repository untouched. See [install.md](install.md#installing-from-inside-the-app).
- The TUI waits for saves and fetches to finish before switching, and asks you to save or discard drafts.
- When something fails, the status line says why, and `!` shows the full output.

## Useful details

- The overview shows progress and suggestions from `bin/next`, marked `[agent]` or `[human]` according to who should do them.
- `y` copies the open, hovered, or ticked item as Markdown; `Y` copies the whole list, batch, or group. If no clipboard tool is available, the TUI writes the text under `data/<owner>/<repo>/exports/`.
- An incremental fetch cannot detect an issue or PR that was deleted or transferred. The next full fetch, run automatically at least daily or manually with `R`, marks items missing from the open list closed.
- Terminals smaller than 60×24 show a resize prompt.

## Review closed-PR notifications

After explicitly enrolling/polling a closed-PR watch through [the watch commands](appeal-evidence.md#explicit-closed-pr-watches), open **Notifications** from the sidebar. The app checks tracked comments at startup and when you press `r` to refresh the ledger. **Needs attention** shows new tracked comments and unviewed saved PR activity or imported actions; **Past actions** shows quiet or viewed tracked items and viewed saved records. Press `v` to mark a selected alert viewed, or one `d` to dismiss a row. Select with `j`/`k` or `Tab` and open with `Enter`. The second screen shows that PR's saved comments or attributed closure explanations as cards with excerpts. `Enter`, `l` or `→` on a card opens its PR item with a fresh read from GitHub, read only; `Esc` or `h` returns to the selected card. Previous/More cards page within the same screen. Reading alone does not mark activity viewed or resolve it.

Check the original rationale, observed state, source gaps and last successful check together through the [offline CLI](appeal-evidence.md#needs-attention-bounded-offline-readers) when a full reassessment is needed. Activity is a review lead, not a confirmed appeal. The TUI excerpts are bounded and omit full source text. Opening the notification list and cards makes no GitHub request. Opening the PR item refreshes its details; reading acknowledges nothing and preserves unsaved decisions. What to do about a closure — reopening it, answering the contributor, leaving it closed — happens outside this tool.

For a requested reassessment from imported closure history through explicit enrollment and source review, follow [review-appeal](../prompts/review-appeal.md). No replacement PR is required; the workflow reports a local reassessment and leaves GitHub actions to separately authorized work.
