# _triage-o-mator_: a first pass through the backlog, without losing the human

**A terminal tool for maintainers and the contributors they trust, facing thousands of open Issues and Pull Requests.** It installs into the repository you triage, organizes a first pass in small batches **—** by a person or an AI agent **—** and keeps every decision reviewable, in git, before anyone acts on it.

> [!IMPORTANT]
> Machines propose while humans decide.

This is how a less technical README I dislike reading would read, which given that I make myself no favors by excluding such I had to write.

<details>

<summary>See some screencaptures</summary>

<img width="1920" height="1032" alt="flow-0" src="https://github.com/user-attachments/assets/f3a3d6cf-b1a7-4495-a803-1272a0c008b4" />

<img width="1920" height="1038" alt="flow-1" src="https://github.com/user-attachments/assets/b6fc6a9d-28b4-488f-af3f-d1f9b4432203" />

</details>

  ## The problem

Popular open source projects collect Issues and PRs faster than anyone can read them.

[omacom/omarchy](https://github.com/omacom/omarchy), where this tool was built and first used, has about **5,000 open issues and PRs** and gets roughly **100 new ones a day**. At that scale nobody reads the backlog from the start. Duplicates pile up, good PRs go stale, and contributors wait weeks for a first response.

The usual fixes fall short:

- **Bots that label and close automatically** act on a live repo without anyone checking them first. One wrong "duplicate, closing" erodes contributor trust quickly.
- **Asking an AI to "triage the repo"** gives answers nobody can audit, spread across chat logs, lost and forgotten.
- **Spreadsheets and ad-hoc scripts** don't hold up past a few hundred items!

## Where the work lives

You clone and build _triage-o-mator_ once, then install it into each repository you triage in a single command.

That creates one `triage-o-mator/` directory inside that repository, and it's as simple as it sounds: its ledger, its review groups, its reports and its taxonomy, with the program itself symlinked back to your checkout of its very own repo. The triage is committed to the repository it belongs to, so a pass of work reaches everyone else as an ordinary pull request that a maintainer reads line by line; with no new place to look, no separate service, no database.

A repository you don't control can carry the install locally instead using `--solo` (one line in `.git/info/exclude`), not a single tracked file touched, and adopt it later with `--adopt` once they recognize your triage-ability, which stages everything ready for the pull request that says **"we use this now."**

## Design principles

1. **Read-only against GitHub.** Every GitHub call is a read. Nothing is labeled, commented on, closed or merged. Acting on a decision is a future step that will need explicit sign-off. At least for now anyhow.
2. **Proposals and reviews are separate states.** A decision is first _triaged_ (by an agent or a person) and then _reviewed_ (only by a person). Nothing crosses that line by itself.
3. **One ledger per repo, tracked in git.** `triage-o-mator/data/<owner>/<repo>/ledger.jsonl`, one JSON line per item, inside the repository it describes, so every decision and every correction shows up as a normal diff.
4. **Scripts underneath, a TUI on top.** Everything the TUI does goes through small, testable `bin/` scripts that agents, cron jobs and people can call directly.
5. **Fixed categories.** Categories, actions and confidence levels come from a small, documented taxonomy that each team owns and edits. Neither human nor clanky model invents one on the fly.

## Main features

### A terminal UI built for reading, not skimming

_triage-o-mator_ is a [Bubble Tea](https://github.com/charmbracelet/bubbletea) app over the ledger:

- **Queues in the sidebar:** Untriaged Issues and PRs, Pending Review, Merge-Ready PRs, Close Candidates, Oldest Untriaged, All Items, then Batches, **Groups** and Possible Duplicates.
- **A full-screen reader** with Body, Agent notes, Comments and Diff tabs.
- **A decision form** whose category, action and confidence cycle through the taxonomy's exact values; with a one-sentence reason for free text.
- **Unsaved drafts per item** for the session, so you can compare several reports before deciding.
- **One key, one meaning, everywhere:** a capital letter is the bigger version of the same action, anything destructive wants the same key twice, and `u` stands for undo, as one would imagine.

- A footer that always shows the keys for the current screen; a breadcrumb that says where you are, plain-language failures (`!` for the full command and output), 15 bundled themes and custom ones without rebuilding!

What more could you even expect these days?

### Batches, from the TUI or command line

The core loop is a **batch**: a manageable number of untriaged items (25 is a good size), fetched with their full bodies and comments. Even an Agent could process this without blowing its virtual brains out.

- Create batches by size, kind, order and group.
- Each batch shows its progress, and decisions saved inside it are hot-iron stamped with its ID.
- When an **AI agent has filled one in**, its proposals show in the list and prefill the form. Review them one at a time, or apply the rest as unreviewed agent decisions. Applying never overwrites a decision someone else already saved.

### Duplicate detection that points you to the other item

Duplicates are the most common and the riskiest call to get wrong. _triage-o-mator_ finds likely candidates and leaves the verdict to the reader.

- Our super-performant, **`bin/similar`** ranks items by title similarity, **offline** on the full backlog (in about 0.1 seconds, for a whole 5,000 set of items), weighting rare terms far above common ones.
- **A Possible Duplicates view** lists every likely pair. You would be surprised with how many of the most common have identical titles!
- **"Not duplicates" is a durable verdict.** Rule a pair out with `d`, and it stays out, recorded in git, so nobody re-litigates it next week.
- **Batches carry the candidates too**, so an Agent can name a duplicate it would otherwise never have seen, and a dedicated, Agentic playbook compares the full bodies and comments of an item and its candidates.

Overall, scores are presented as leads instead of proof.

### Review groups for team handoffs

Related issues and PRs often need to be read together: a bug report, its duplicates, and two competing fixes. **Groups** hold them with a description, an assignee, a draft / ready / archived status, per-item notes, and who contributed what.

- Add items from anywhere, browse members, open any for full context.
- Export **Markdown or JSON review packets** with current decisions and cross-references, optionally with live bodies, comments and diffs, to hand to a co-maintainer.
- Group files are written atomically under a lock and protected by revision checks, so a stale edit is rejected instead of silently overwriting someone else's.

### Fetching that keeps up with a busy repo

- **Incremental by default:** the difference between a full fetch and an incremental one is by a factor of about 80! It is so fast, I couldn't even finish this sen.
- **Full fetches** run automagically at least once a day. They catch deleted or transferred items.

### Agents as collaborators, not authorities

- **A core playbook ships with the install** becoming that install's `AGENTS.md` for the tool, and without disturbing yours. All Agents are supported.
- **One playbook per task:** triage a batch, check duplicates, review a PR's code, organize groups, make a report decision-ready, and brief the maintainers. Ask in plain words and the Agent will pick the right one for you.
- **The code is right there.** Because the install sits inside the repository, an agent reviewing a PR reads the actual files to check broader context and whether a fix already landed.
- **`y` hands a screen to an agent.** Press it anywhere and what's in front of you (an item with its body and decision, ticked items, a whole batch with its file paths) is then on your clipboard as Markdown, ready to paste into a chat.

> [!WARNING]
> Our documentation is open about the risk: issue text comes from unfiltered users and can contain prompt-injection attempts.
> Running YOLO Agents in an isolated VM is recommended.

### Reporting, and a report you can decide from

`bin/report` writes a dated Markdown snapshot: ready groups first, then human-confirmed decisions by action, the review queue, merge-ready PRs, close candidates and the oldest untriaged. It's meant to be committed, so the repo keeps a history of its own backlog.

"But hey!," you may ask, "Shouldn't each item be re-checked against its current state and the code, then a recommendation with the case **for** and **against** it, and the diff hunk or comment that settles it be added on top?"

Yes, and so reports coupled with our revolutionary `polish-report` prompt, turns a section of one into decisions, giving you the perfect way to brief those pesky lead maintainers that ignore your PR for half a year.

> [!NOTE]
> The name of the prompt for reports has nothing to do with Vaxry. He is a well-respected and virile member of the developer community.

Spreadsheet work with CSV files is also supported!

## Quality and safety

- **Go and Python test suites** cover the TUI's layout at several terminal sizes, its keyboard flows and each screen, plus the scripts' behavior and the install layout, in throwaway checkouts and repositories with a fake `gh`. None of them can touch a real install or GitHub.
- **`make check`** runs the tests, `go vet`, `gopls`, `gofmt` and Prettier; the toolchain is pinned with [mise](https://mise.jdx.dev/).

## Getting started

```sh
git clone https://github.com/efrmg/triage-o-mator && cd triage-o-mator
./install.sh /path/to/your/repository # builds, then installs
cd /path/to/your/repository && ./triage-o-mator/bin/triage-o-mator
```

It needs an authenticated `gh`, `mise` and `python3`, which is a shorter list than the commands above.

Installing into a repository you don't control, adopting an install later, and making the prompts and taxonomy your own are all in `docs/install.md`.

## What's next

- **Team-scale triage:** handing out non-overlapping batches and resolving conflicting decisions on the same item.
- **Duplicate detection that reads bodies,** through a local cache or embeddings.

## How you can help

- **Try it on a repo you maintain** and report where the taxonomy or the workflow doesn't fit.
- **Take on something from the roadmap.** The path to write on the remote in particular deserves careful design and review.

---

The goal is not to replace maintainer judgment but to complement it. We can make sure every issue and PR gets read and decided upon more easily, along with other minor things such as conquering the world shortly after.
