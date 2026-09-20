# Installing into a repository

This is the design note the install layout was implemented from.

## How to

You clone and build triage-o-mator once, then install it into each repository you triage:

```sh
git clone https://github.com/efrmg/triage-o-mator && cd triage-o-mator
./install.sh /absolute/path/to/target-repo # mise install, make build, bin/install-to
```

`bin/install-to <path>` creates one directory in the target repo and touches nothing else outside it:

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

You then work from the target repo: `triage-o-mator/bin/triage-o-mator` for the TUI, `triage-o-mator/bin/next`, and so on.

The rule behind what is symlinked and what is copied: **code and defaults are symlinked** so every install upgrades the moment you rebuild the tool; **policy and data are copied or created** because they belong to the target repo and are meant to be reviewed in its diffs.

## Write access, or not

`config/repo` defaults to the target's `upstream` remote when present, then its `origin`, so both a maintainer's clone and a contributor's fork need no flag: the triage record lives with the code while its GitHub reads target the original repository.

**Adopted (default, you have write access).** The directory is committed to the target repo. Contributors get decisions by pulling; they hand work to maintainers by opening a normal PR that changes `triage-o-mator/data/<owner>/<repo>/ledger.jsonl` and `groups/`. Review of a triage pass is a diff review, which is what the ledger's JSON Lines format was chosen for. The installer also adds a short block to the target's `AGENTS.md` (and `CLAUDE.md` if present) so agents working in that repo find the playbook.

**Solo (`--solo`, a repo you don't control).** You are usually in a fork or a plain clone. The installer writes `triage-o-mator/` to `.git/info/exclude` instead of the repo's `.gitignore`: a local, untracked ignore, so the install changes **no tracked file** of a repo that isn't yours. It skips the `AGENTS.md` block for the same reason (`--agents-md` forces it, and warns that it will show up as a local modification). You triage, and what you hand to maintainers is `bin/report` output and group packets, not commits. If the maintainers would rather ignore the directory openly, a `triage-o-mator/` line in their `.gitignore` is equivalent and visible to everyone.

**Adoption (`--adopt`).** Turns a solo install into the tracked one: drops the `.git/info/exclude` line, writes the `.gitignore` and the `AGENTS.md` block, and stages the directory so you can open a "adopt triage-o-mator" PR carrying the backlog work you already did. This is the intended path from "one contributor tries it" to "the project uses it".

**Handing an install to someone else?** The data in the directory is portable; the wiring is not, because symlinks are absolute. Whoever receives it runs `bin/install-to <path>` against their own checkout, which repairs the symlinks in place and leaves everything tracked untouched. Re-running the installer is always safe and is how you upgrade, repair, and re-point an install.

## Prompts, taxonomy, and customizing

`config/taxonomy.json` and `config/taxonomy.md` are **copies**, tracked in the target: every team edits its taxonomy, and it has to be reviewable there. Re-running the installer never overwrites them; when the tool's own taxonomy has changed since, it writes `taxonomy.json.dist` beside yours to diff against.

`prompts/` is symlinked **per file**, not as a directory. A directory symlink would mean that adding a prompt in one install writes into the tool checkout and appears in every other install. With per-file symlinks:

- replacing a prompt: delete the symlink, write a real file in its place. The installer never overwrites a real file, and leaves it out of the generated ignore list, so it is tracked with the rest of the install.
- adding a prompt: just write it. Same result.
- getting an upstream prompt back: delete your file and re-run the installer.

This is why `triage-o-mator/.gitignore` is **generated**: the installer knows exactly which files it symlinked and lists them between markers on each run, so symlinks stay out of Git (an absolute symlink committed to a repo is broken for everyone else) while your own prompts stay in.

The checkout's `/data/` and `/reports/` ignore rules went too, once it was clear nothing could write either path there: `WORK_ROOT` is always an install and the scripts exit outside one, so the guarantee is structural rather than a gitignore entry. They had been kept as a guard for checkouts from before the cutover, and there were none left.

## Known sharp edges

- **Absolute symlinks** break when the tool checkout moves or is deleted; a dangling symlink otherwise surfaces as a bare "No such file or directory". `bin/*` and the TUI should detect it and say "the triage-o-mator checkout has moved — re-run bin/install-to".
- **Windows** has no usable symlinks without developer mode. Out of scope for now; document it.
- **Ledger concurrency is unchanged.** Installs make sharing easier, not concurrent editing safer: `ledger.jsonl` still has a single-writer workflow (README, "Not built (yet)").
