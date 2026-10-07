# Installing triage-o-mator into a repository

triage-o-mator is installed into the repository you want to triage. You clone and build it once, and every repository you triage gets its own `triage-o-mator/` directory holding that repository's ledger, groups, reports and taxonomy, wired to your one checkout.

The checkout is the program; the install is the work. Install-scoped scripts require an install. Without one, the TUI opens **Switch Repo** so you can choose or create an install.

## Install it

```sh
gh repo clone efrmg/triage-o-mator && cd triage-o-mator
./install.sh /absolute/path/to/your/repository
```

`install.sh` is `mise install`, `make build`, and `bin/install-to` in one command. Once built, install into further repositories with `bin/install-to /path/to/another/repository` directly. You need [`gh`](https://cli.github.com/), authenticated (`gh auth status`), and `python3`.

Then run it from the repository, which is where most people already are:

```sh
cd /absolute/path/to/your/repository
triage-o-mator/bin/triage-o-mator # the TUI
triage-o-mator/bin/next # what to do next
```

Every script finds its install from its own path, not from your working directory, so this works from the repository's root, from any directory under it, or from inside `triage-o-mator/` itself. What they print adapts to where you ran them: `triage-o-mator/bin/next` from the root suggests `triage-o-mator/bin/batch 25`, and `bin/next` from inside the install suggests `bin/batch 25`. Either way, what it prints is what you can paste.

## What lands in your repository

```
target-repo/
  .git/info/exclude            # solo mode only: one local line, no tracked file changed
  AGENTS.md                    # tracked mode: a short marked block pointing at triage-o-mator/AGENTS.md
  triage-o-mator/
    bin -> ~/src/triage-o-mator/bin          symlink   ignored
    themes -> ~/src/triage-o-mator/themes    symlink   ignored
    AGENTS.md -> ~/src/triage-o-mator/…      symlink   ignored
    prompts/auto-triage.md -> …              symlink   ignored (one per prompt)
    config/repo                              generated tracked
    config/taxonomy.json                     copy      tracked
    config/theme.local                                 ignored
    data/<owner>/<repo>/ledger.jsonl                   tracked
    data/<owner>/<repo>/groups/, not-duplicates.jsonl  tracked
    data/<owner>/<repo>/briefed-batches/               tracked batch screening checkpoints
    data/<owner>/<repo>/master-briefs/                tracked master brief input records
    data/<owner>/<repo>/action-proposals/            tracked proposals
    data/<owner>/<repo>/action-assessments/          tracked selected action assessments
    data/<owner>/<repo>/duplicate-assessments/       tracked pair comparisons
    data/<owner>/<repo>/pr-assessments/              tracked PR reviews and closure assessments
    data/<owner>/<repo>/raw|batches|exports/           ignored
    data/<owner>/<repo>/cache|local/                   ignored (includes the per-repo automatic download preference)
    reports/<owner>/<repo>/<date>.md                   tracked report
    reports/<owner>/<repo>/<date>-batch-<id>-brief.md  tracked batch brief
    reports/<owner>/<repo>/<date>-<kind>-<N>-brief.md   tracked item brief
    reports/<owner>/<repo>/<date>-master-brief.md      tracked master brief
    .gitignore                               generated tracked
    .triage-install.json                     generated ignored (machine-local: where the tool lives)

```

The install carries `AGENTS.md` and `CLAUDE.md`, both leading to the same playbook. Decisions, groups, duplicate verdicts, batch briefing checkpoints, reports and taxonomy are committed. Symlinks, the install marker, working files and cache are ignored; preserve the cache separately when retained evidence depends on it.

A tracked install creates the target repository's `AGENTS.md` when it is missing, even if that repository already has a `CLAUDE.md`; the existing `CLAUDE.md` stays untouched. Editing an existing `AGENTS.md` still asks for confirmation unless you pass `--yes`. Use `--no-agents-md` to skip the target file, or `--agents-md` to add it in solo mode.

`config/repo` defaults to what the repository's `upstream` remote points at when it has one, falling back to `origin`. This makes a clone of your fork triage the original repository's backlog. Pass `--repo owner/repo` to triage something else from here. An existing install keeps its recorded repo; if it was created from a fork before upstream detection was added, re-run `bin/install-to /path/to/repository --repo owner/repo` once to correct it.

Installation reads that repository's GitHub label definitions into `config/taxonomy.json` through a read-only GET. `label_catalog.observed_at` records the successful observation time. `--offline` skips the read; a new catalog stays `pending`, while an existing catalog keeps its dated observation. A failed GitHub read also leaves the install usable with a pending or previously observed catalog. Run `triage-o-mator/bin/label-catalog sync --expected-repo OWNER/REPO` for an explicit online refresh, or `show` to inspect the saved catalog offline. `sync --dry-run` previews the result without saving it. The sync retains local `guidance` by label ID, records prior names, and moves removed labels into `retired`; it never changes label definitions on GitHub or rewrites saved decisions.

The TUI's **Settings** menu has separate **Labels**, **Actions** and **Automations** cards. Labels and Actions accept `n` to create an entry and `e` or Enter to edit the selected entry in a floating editor. Action editors add a GitHub operation field: use Tab to focus it and Left or Right to choose `comment`, `close`, `reopen`, or the explicit `none` choice. An action without a recognized operation is visible in Settings but cannot be selected for a new decision until mapped. `Ctrl-E` opens `$EDITOR` for the focused Title or Description field. **Actions** saves locally through `bin/taxonomy-settings`; choosing an action or reviewing a decision does not write to GitHub. **Labels** changes GitHub label definitions: `Ctrl-P` reads current GitHub values and previews them alongside the proposed values, then `Ctrl-S` confirms the change. From the editor, `Ctrl-S` opens that preview first, so a second `Ctrl-S` confirms it. A changed GitHub label invalidates the preview. New individual labels use a neutral `ededed` color. Press `r` in **Labels** to refresh the saved catalog. Press `i` to keep GitHub's current labels and add missing defaults; `Ctrl-S` confirms the exact additions. Press capital `I` to make GitHub match the saved local catalog, including custom labels; its preview lists creations, edits, and deletions before `Ctrl-S` confirms them. Deleting a label removes it from issues and PRs. A pending catalog still allows action editing and individual label creation, but cannot serve as the source for capital `I`.

`bin/label-definitions --expected-repo OWNER/REPO --name NAME --description TEXT` previews a new label without writing it. To edit one, also pass its `--label-id`, `--expected-name`, and `--expected-description` from the saved list. The output contains a `preview_sha256`; repeating the exact command with `--apply --preview-sha256 HASH` rechecks GitHub and applies that preview. The Settings editor handles these steps interactively.

`bin/label-definitions --expected-repo OWNER/REPO --initialize-defaults` previews the missing starter labels. Repeat it with `--apply --preview-sha256 HASH` from that preview to create them. The script rechecks the live catalog before writing and refreshes the local catalog after each successful creation, so a partial run remains visible. See [taxonomy guidance](taxonomy.md) for the starter set and decision guidance.

`bin/label-definitions --expected-repo OWNER/REPO --reconcile-local` previews the exact changes needed to make GitHub match the install's saved label catalog. Repeat it with `--apply --preview-sha256 HASH` to apply that reviewed plan. The script rechecks the local file and live labels before each write. If a write stops partway through, the local catalog stays authoritative; preview again before retrying.

### Automations and item labeling

**Settings → Automations** has Labeling and Scoring cards. Labeling is ON by default for the selected repository; Enter or Space turns it OFF or back ON. Press `y` on Labeling to copy a pinned prompt for an agent to follow `prompts/label-items.md`. Press `y` on Scoring to copy a bounded, offline quality/readiness pass prompt; [Item Score](item-score.md) is saved locally and has no GitHub write mode. Opening either card never starts a pass.

`proposed_labels` in the ledger is a local decision, separate from the item's observed GitHub `labels`. The agent's labeling pass previews a dry-run list of current labels, proposals, exact additions and removals before running. The script checks the selected repository, current ledger proposals and live GitHub labels again before each write. The pass accepts attributed local proposals regardless of an explicit Pending review request and never clears that request.

The CLI exposes larger bounded passes: `bin/item-labels preview --expected-repo OWNER/REPO --limit N --request-budget B` returns a `preview_sha256`; `bin/item-labels run` with the same repository, limit, budget and `--preview-sha256 HASH` revalidates and applies it. Repeat `--key issue:N` or `--key pr:N` on both commands to restrict the pass to exact items; the preview hash binds that selection. `N` is at most 50 and the budget at most 500 GitHub requests. Items without observed labels are considered first, and checked items rotate behind unchecked ones so repeated bounded passes can cover a larger backlog. Initial passes add missing proposed labels while preserving other labels. A later proposal change can remove only labels that this pass previously added. The per-item state and append-only outcomes are kept under ignored `data/<owner>/<repo>/local/`; a changed live label set pauses that item so a later pass does not override a human correction. An uncertain write stays pending. After inspecting an affected item, `bin/item-labels reset-item --expected-repo OWNER/REPO --kind issue|pr --number N` previews a new baseline; repeat it with `--apply --preview-sha256 HASH` to resume from the current live labels without claiming ownership of them. Turn Labeling OFF from its Automations card with Enter or Space. A human edit between the final GitHub read and write can still race that write because GitHub does not provide a shared transaction with this local ledger; inspect the recorded outcome and live labels if that happens.

## Working from a fork

The local checkout, the backlog being read, and the destination for triage commits can be different. For example, you can keep the install and its commits in `efrmg/omarchy` while reading issues and PRs from `omacom/omarchy`. Upstream item numbers always remain upstream item numbers; the tool does not copy those items into your fork.

Start by cloning the fork. This example uses `efrmg/omarchy`; substitute your own fork and local path as needed:

```sh
gh repo clone efrmg/omarchy /absolute/path/to/omarchy
```

Then, from the built triage-o-mator checkout, install into that clone with an explicit backlog target:

```sh
bin/install-to /absolute/path/to/omarchy --repo omacom/omarchy --dry-run
bin/install-to /absolute/path/to/omarchy --repo omacom/omarchy
```

The preview shows the install location, backlog target, mode, and files that will change. Each repository's data lives under its own `data/<owner>/<repo>/` directory.

Verify the selected backlog and launch from the fork. The config command should print `omacom/omarchy`:

```sh
cd /absolute/path/to/omarchy
cat triage-o-mator/config/repo
triage-o-mator/bin/triage-o-mator
```

Choose how to share your work:

- **Tracked:** the default for a new install. Keep the ledger, groups, and reports in a dedicated branch of your fork, inspect the diff, and share a PR when appropriate. Upstream adoption would be a separate decision by its maintainers.
- **Solo:** add `--solo` to both the preview and install commands to keep the install out of the clone's Git history. Share reports and group packets instead. Ignored data is not backed up by committing the fork.
- **Adopt an existing solo install:** use `--adopt --repo omacom/omarchy`, previewing with `--dry-run` first. Adoption stages the install's tracked files; inspect `git diff --cached` before committing.

A remote named `upstream` is optional when `--repo` is explicit. If you want automatic detection for a new install, inspect `git remote -v` and add `git remote add upstream https://github.com/omacom/omarchy.git` only if that remote is absent. The installer does not fetch the remote or update your branch. Existing installs retain their recorded target until explicitly changed.

Use `--repo efrmg/omarchy` only when you intend to triage the fork's own backlog. Access to the upstream backlog is sufficient for this read-only workflow; permission to push to your fork does not grant permission to change upstream issues or PRs. When reviewing code, verify the PR's actual upstream base revision rather than assuming the fork's checked-out branch is current.

To correct a backlog target later, re-run `bin/install-to` with the intended `--repo`. Re-running preserves the install's mode and retains the previous target's data in its own directory; it does not reassign those ledger rows to the new target.

## A repository you don't control

Installing into a clone of a repository you can't push to would still leave the install in `git status` for everyone who later pulls your branch. `--solo` avoids that: the install is listed in `.git/info/exclude`, which is local to your clone, so **no tracked file of that repository changes** and the `AGENTS.md` block is skipped (`--agents-md` adds it anyway, as a local modification you'll see in `git status`).

In a linked worktree, this exclude file is shared by all worktrees of the clone, so adding or removing the solo exclusion affects them all.

You then triage on your own and hand maintainers `bin/report` output and group packets, rather than commits. If they would rather ignore the directory openly, a `triage-o-mator/` line in the repository's own `.gitignore` does the same thing visibly.

When they want it, `bin/install-to <path> --adopt` turns that into an install the repository keeps: the local exclude goes, the `AGENTS.md` block is written, and everything the install tracks is staged, ready to commit as the pull request that adopts the tool, carrying the backlog work you already did.

## Installing it into triage-o-mator itself

`bin/install-to .` from the checkout installs the tool into its own repository, so the project triages its own backlog with the code in your working tree: the install is `triage-o-mator/triage-o-mator/`, its `bin/` symlinks back to `../bin`, and its ledger and reports are committed to this repository like any other adopted install. The `AGENTS.md` block it writes is a shorter one, since the checkout's `AGENTS.md` is already the playbook the usual block points at.

This is the one case where the checkout holds triage data, and it stays inside the nested install. No script uses top-level `/data/` or `/reports/` paths in the checkout.

## Opening the app without an install

Running `bin/triage-o-mator` where there is no install (in a fresh checkout, say) opens **Switch Repo** and nothing else: the installs recorded on this machine, a filter, and a field that takes a path. See the [TUI tutorial](tutorial.md#13-move-between-repositories-without-mixing-their-work) for its controls.

## Working as a team through it

Once the install is tracked, triage is ordinary repository work: contributors pull to get each other's decisions, and hand work to maintainers by opening a pull request that changes `triage-o-mator/data/<owner>/<repo>/ledger.jsonl` and `groups/`. The ledger is JSON Lines so the diff is readable line by line. Everyone who clones the repository runs `bin/install-to` against it once to wire up their own symlinks; preview the plan if the installer also proposes a tracked update.

See the [triage playbook](../prompts/PLAYBOOK.md#working-as-a-team) for how batches, proposals and reviews divide between people and their agents.

## Upgrading, repairing, moving

Re-running `bin/install-to` on an existing install is always safe, and is how you:

- **upgrade**: `git pull && mise exec -- make build` in the checkout, then re-run it. The scripts are already the new ones, since `bin/` is a symlink, but a playbook added upstream is linked file by file, so it only appears in this install once `bin/install-to` runs here again.
- **repair**: after the checkout moved or was re-cloned, the install's symlinks point nowhere. Re-running relinks them.
- **re-point**: run it from a different checkout to wire the install to that one.
- **hand it over**: the install's data is portable, its symlinks are not. Whoever receives it (or clones the repository that adopted it) runs `bin/install-to` against it from their own checkout.

`--dry-run` prints every change first and writes nothing.

Upgrades also maintain a separate cache/local ignore block, including derived `reposition/` indexes and preserving rules outside the managed markers. This may change the tracked install `.gitignore`; inspect its diff. Malformed markers and unsupported future install versions are refused before changes. Stop older writers before upgrading, and see [evidence-cache compatibility and backups](evidence-reference.md#upgrade-and-backup).

To move or back up an install, preserve its `config/`, `data/` and `reports/`; `config/` includes the repository target and the team's taxonomy. For a Git-based handoff, back up the ignored cache separately when retained evidence, watches or imported closure records depend on it. Re-run `bin/install-to` at the new location to repair machine-specific links.

## Making it yours

`config/taxonomy.json` is committed with your repository. The installer reconciles its label catalog from GitHub on each online run while preserving local label guidance and the existing action list. General guidance lives in the linked [taxonomy guide](taxonomy.md). Local label guidance can be edited in JSON until Settings provides an editor for it.

Prompts are symlinked one file at a time, so you can make them yours without touching the checkout. A directory symlink would put edits into the shared tool checkout and affect every install. The installer regenerates the ignored-symlink list between managed markers while leaving your own files visible to Git:

- **replace one**: delete the symlink and write a file in its place. It is then yours, committed with the rest.
- **add one**: just write it into `prompts/`.
- **go back to the checkout's**: delete your file and re-run `bin/install-to`.

## Switching between installs

`bin/install-to` records every install it creates in `~/.config/triage-o-mator/installs.json`. The TUI's **Switch Repo** lists this install's repos first, then every other registered install's, and moving to one of those moves the whole session (its taxonomy, its theme, its ledgers). Typing filters that list; an absolute path opens an install directly, including the path of the repository that holds one.

## Installing from inside the app

Opening the path of a repository that has no install runs `bin/install-to --dry-run` and shows exactly what that script would change, with nothing written. The plan can be switched between tracked and solo modes; after confirmation, the session opens the new install. The [tutorial](tutorial.md#13-move-between-repositories-without-mixing-their-work) gives the controls.

Confirming the plan passes `--yes`, because the reviewed plan is the confirmation; the app never asks the script to do anything the plan did not list. The plan defaults to a tracked install, as the command line does, so the ledger can be shared through Git.

## When something is wrong

- **"is not a triage-o-mator install:"** you are running the scripts from the checkout, or from a clone where nobody has run `bin/install-to` yet. Run it against the repository.
- **"No such file or directory" from a script, or a dangling `triage-o-mator/bin`:** the checkout moved, was deleted, or was never built. Re-run `bin/install-to` from it; `make build` if the TUI itself is missing.
- **`TRIAGE_ROOT`** overrides which install every script and the TUI use, for scripting and tests. The TUI also takes `--root /path/to/an/install`.
- **Windows** needs developer mode for symlinks; the install layout assumes they work. WSL is the tested path there.
