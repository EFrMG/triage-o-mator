# Installing triage-o-mator into a repository

triage-o-mator is installed into the repository you want to triage. You clone and build it once, and every repository you triage gets its own `triage-o-mator/` directory holding that repository's ledger, groups, reports and taxonomy, wired to your one checkout.

The checkout is the program; the install is the work. Nothing runs outside an install: the scripts and the TUI look for one and say so when there is none.

## Install it

```sh
git clone https://github.com/efrmg/triage-o-mator
cd triage-o-mator
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
  AGENTS.md                    # adopted mode only: a short marked block pointing at triage-o-mator/AGENTS.md
  triage-o-mator/
    bin -> ~/src/triage-o-mator/bin          symlink   ignored
    themes -> ~/src/triage-o-mator/themes    symlink   ignored
    AGENTS.md -> ~/src/triage-o-mator/…      symlink   ignored
    prompts/auto-triage.md -> …              symlink   ignored (one per prompt)
    config/repo                              generated tracked
    config/taxonomy.json, taxonomy.md        copies    tracked
    config/theme.local                                 ignored
    data/<owner>/<repo>/ledger.jsonl                   tracked
    data/<owner>/<repo>/groups/, not-duplicates.jsonl  tracked
    data/<owner>/<repo>/raw|batches|exports/           ignored
    reports/<owner>/<repo>/<date>.md                   tracked
    .gitignore                               generated tracked
    .triage-install.json                     generated ignored (machine-local: where the tool lives)

```

Every agentic tool reads `AGENTS.md` and Claude Code reads `CLAUDE.md`, so the install carries both and they lead to the same playbook. What is committed is what your team needs from each other: decisions, groups, duplicate verdicts, reports, and the taxonomy you agreed on. What is not is either wired to a machine (symlinks, the marker) or re-creatable on demand (the GitHub cache, batches, exports).

`config/repo` defaults to what the repository's `upstream` remote points at when it has one, falling back to `origin`. This makes a clone of your fork triage the original repository's backlog. Pass `--repo owner/repo` to triage something else from here. An existing install keeps its recorded repo; if it was created from a fork before upstream detection was added, re-run `bin/install-to /path/to/repository --repo owner/repo` once to correct it.

## A repository you don't control

Installing into a clone of a repository you can't push to would still leave the install in `git status` for everyone who later pulls your branch. `--solo` avoids that: the install is listed in `.git/info/exclude`, which is local to your clone, so **no tracked file of that repository changes** and the `AGENTS.md` block is skipped (`--agents-md` adds it anyway, as a local modification you'll see in `git status`).

You then triage on your own and hand maintainers `bin/report` output and group packets, rather than commits. If they would rather ignore the directory openly, a `triage-o-mator/` line in the repository's own `.gitignore` does the same thing visibly.

When they want it, `bin/install-to <path> --adopt` turns that into an install the repository keeps: the local exclude goes, the `AGENTS.md` block is written, and everything the install tracks is staged, ready to commit as the pull request that adopts the tool, carrying the backlog work you already did.

## Installing it into triage-o-mator itself

`bin/install-to .` from the checkout installs the tool into its own repository, so the project triages its own backlog with the code in your working tree: the install is `triage-o-mator/triage-o-mator/`, its `bin/` symlinks back to `../bin`, and its ledger and reports are committed to this repository like any other adopted install. The `AGENTS.md` block it writes is a shorter one, since the checkout's `AGENTS.md` is already the playbook the usual block points at.

This is the one case where the checkout holds triage data, and it stays inside the install: the checkout's own `/data/` and `/reports/` remain ignored, so a fork still cannot commit a ledger for whatever else its owner triages.

## Opening the app without an install

Running `bin/triage-o-mator` where there is no install (in a fresh checkout, say) opens **Switch Repo** and nothing else: the installs recorded on this machine, a filter, and a field that takes a path. `Esc` quits.

## Working as a team through it

Once the install is tracked, triage is ordinary repository work: contributors pull to get each other's decisions, and hand work to maintainers by opening a pull request that changes `triage-o-mator/data/<owner>/<repo>/ledger.jsonl` and `groups/`. The ledger is JSON Lines precisely so that diff is readable line by line. Everyone who clones the repository runs `bin/install-to` against it once to wire up their own symlinks; nothing tracked changes when they do.

See [AGENTS.md](../AGENTS.md#working-as-a-team) for how batches, proposals and reviews divide between people and their agents.

## Upgrading, repairing, moving

Re-running `bin/install-to` on an existing install is always safe, and is how you:

- **upgrade**: `git pull && mise exec -- make build` in the checkout, then re-run it. The scripts are already the new ones, since `bin/` is a symlink, but a playbook added upstream is linked file by file, so it only appears in this install once `bin/install-to` runs here again.
- **repair**: after the checkout moved or was re-cloned, the install's symlinks point nowhere. Re-running relinks them.
- **re-point**: run it from a different checkout to wire the install to that one.
- **hand it over**: the install's data is portable, its symlinks are not. Whoever receives it (or clones the repository that adopted it) runs `bin/install-to` against it from their own checkout.

`--dry-run` prints every change first and writes nothing.

Moving an install's data somewhere else is a plain directory move: `data/<owner>/<repo>/` and `reports/<owner>/<repo>/` are the whole record, and an install picks them up wherever it finds them.

## Making it yours

`config/taxonomy.json` and `config/taxonomy.md` are copies, committed with your repository: edit them freely. A re-run never overwrites them; when the checkout's version has changed it leaves `taxonomy.json.dist` beside yours to diff against.

Prompts are symlinked one file at a time, so you can make them yours without touching the checkout:

- **replace one**: delete the symlink and write a file in its place. It is then yours, committed with the rest.
- **add one**: just write it into `prompts/`.
- **go back to the checkout's**: delete your file and re-run `bin/install-to`.

## Switching between installs

`bin/install-to` records every install it creates in `~/.config/triage-o-mator/installs.json`. The TUI's **Switch Repo** lists this install's repos first, then every other registered install's, and moving to one of those moves the whole session (its taxonomy, its theme, its ledgers). Typing filters that list; an absolute path opens an install directly, including the path of the repository that holds one.

## Installing from inside the app

Typing the path of a repository that has no install and pressing `Enter` does not install anything: it runs `bin/install-to --dry-run` and shows you exactly what that script says it would change, line by line, with the mode it would use and nothing written. From there, `Enter` makes exactly those changes, `s` shows the same plan for the other mode (tracked or `--solo`) so you can compare before choosing, `j`/`k` scroll a plan longer than the screen, and `Esc` leaves the repository as it was. When the install is made, the session opens it.

`Enter` on the plan passes `--yes`, because the plan you just read is the confirmation; the app never asks the script to do anything the plan did not list. The plan defaults to a tracked install, as the command line does: it is the common case, and choosing `--solo` quietly would leave a ledger nobody else can see, which is a worse surprise than an `AGENTS.md` diff you can read. Either way you see the plan first.

## When something is wrong

- **"is not a triage-o-mator install:"** you are running the scripts from the checkout, or from a clone where nobody has run `bin/install-to` yet. Run it against the repository.
- **"No such file or directory" from a script, or a dangling `triage-o-mator/bin`:** the checkout moved, was deleted, or was never built. Re-run `bin/install-to` from it; `make build` if the TUI itself is missing.
- **`TRIAGE_ROOT`** overrides which install every script and the TUI use, for scripting and tests. The TUI also takes `--root /path/to/an/install`.
- **Windows** needs developer mode for symlinks; the install layout assumes they work. WSL is the tested path there.
