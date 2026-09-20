# Tutorial: the triage-o-mator TUI

A short tour of the TUI. Follow the sections in order; each takes a minute or two. [README.md](README.md) explains what the project is, and [docs/keybindings.md](docs/keybindings.md) lists every key.

Install it into the repository you want to triage ([docs/install.md](docs/install.md)), then launch it from there:

```sh
cd /path/to/your/repository
./triage-o-mator/bin/triage-o-mator
```

Saves, approvals and group edits change your repository's git-tracked ledger and groups for real. To practice, work on a throwaway branch (`git switch -c tutorial`) and drop it afterwards.

## 1. Getting around

- The overview shows how far triage and review have come, and what to do next.
- `j`/`k` move, `Enter` opens, `Esc` goes back. The breadcrumb in the top border shows where you are.
- The footer lists the keys for the current screen; `?` explains them.
- `t` picks a theme; `/` searches the theme names. `r` fetches the latest changes from GitHub.

## 2. Reading an item

- Open any item from a list in the sidebar.
- Its tabs (Body, Comments, and Diff for PRs) run across the top: `H`/`L` switch between them, `Enter` shows one full screen.
- `o` opens the item on GitHub.
- In any list, `/` searches by title words or `#number`.

## 3. Deciding

- In an untriaged item, `l` moves into the form: Category, Action, Confidence and Reason.
- On a choice, `j`/`k` change the value and `Enter` moves to the next field.
- `Ctrl-S` saves. You're back in the list, on the next item.
- Unsaved edits are kept as drafts while you look at other items, marked `unsaved` in yellow in the lists; `q` warns before discarding them.

## 4. Approving

A decision counts only once a human approves it.

- In **Pending Review**, `a` approves the item in front of you.
- `u` undoes one step: first the approval, then the decision. Right after `a`, it acts on the item you just approved, even though it has left Pending Review.
- `Space` ticks items in any list, so `a`, `u` and other actions work on all of them at once.

## 5. Handing context to an agent

- On any screen, `y` copies what is in front of you as Markdown: the item you have open, the ones you ticked, or the one under the cursor. `Y` copies the whole screen's worth — the list, the batch, the group.
- Paste it into your agent's chat and ask about it ("is this a duplicate?", "which of these are safe to merge?"). The block names the repo, the install, and the commands to read more, so the agent can go deeper on its own.
- If nothing on the machine takes a clipboard, the status line tells you the file it wrote instead.

## 6. Reviewing an agent's batch

- Ask your agent to "triage 10 items but don't apply them, I'll check them in the TUI".
- Open it in **Batches**: each item is prefilled with the agent's proposal and its notes.
- Accept a proposal with `Ctrl-S`, or change it first.
- `A` applies the rest as proposals awaiting review. Anything you've saved yourself is kept.
- `n` in **Batches** makes a new batch; `Enter` on its last field creates it.

## 7. Duplicates

- On an item, `m` compares it with lookalike items. That item is the top card, the original the others are judged against: `k` reaches it and `Enter` reads it, so you can compare without leaving.
- On a candidate, `d` `d` rules it out as a duplicate of the item on top; `m` marks that candidate a duplicate of the item on top, with its form prefilled; if it's the older of the two, the status says so. `M` swaps the two, to close the top item instead. `Ctrl-S` saves, from the item or from the comparison, and you stay on the comparison to work through the rest.
- **Possible Duplicates** in the sidebar lists lookalike pairs across the repo, older item first. `Enter` or `m` compares one. Pairs you resolve are marked `handled` until you open the list again; `D` `D` clears them from view. `d` `d` rules a pair out as "checked, not duplicates", which is kept in the repo's data, so nobody is offered it again.

## 8. Groups

A group collects related items so maintainers can decide on them together ([docs/groups.md](docs/groups.md)).

- In **Groups**, `n` creates one.
- On any item, `b` adds it to a group with a note; `B` adds it to the last group used.
- `e` edits a group, including its status: draft, ready (for maintainers) or archived.
- `x` exports a group as a review packet.

## 9. Other repos and failures

- **Switch Repo** moves to another repo. Each repo keeps its own ledger, batches and groups.
- When something fails, the status line says why, and `!` shows the full output.
