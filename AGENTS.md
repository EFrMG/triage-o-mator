# AGENTS.md

triage-o-mator is tooling for working through a GitHub issue and pull request backlog too large for one person to read cold: small scripts and a terminal UI over a git-tracked ledger of triage decisions.

**This file is for working on the tool itself**, in a checkout of this repository. Triaging an actual backlog is a different job with its own playbook, [`prompts/PLAYBOOK.md`](prompts/PLAYBOOK.md), which `bin/install-to` links into every install as that install's `AGENTS.md`. If you have been asked to triage issues, read that one file; otherwise, how any of this works is explained in this current document.

[README.md](README.md) is the user-facing overview.

## What is here

| path                      | what it is                                                                                                       |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `bin/`                    | the scripts that do the work; `_triage.py` and `_install.py` are shared modules, everything else is a CLI        |
| `tui/`                    | the Go terminal UI, built to `bin/triage-o-mator`                                                                |
| `prompts/`                | operating prompts for agents: `PLAYBOOK.md` (main one), and one file per task                                    |
| `config/`                 | `taxonomy.json` and `taxonomy.md`, the default categories and actions that `bin/install-to` copies into installs |
| `themes/`                 | color palettes                                                                                                   |
| `docs/`                   | The documentation installs link back to                                                                          |
| `tests/`, `tui/*_test.go` | Python and Go tests; both build throwaway checkouts and installs with a fake `gh`                                |

There is no `config/repo`, no ledger and no reports here, see why in the following section.

## The install model

The checkout is the program; the work lives in an **install**: the `triage-o-mator/` directory `bin/install-to` creates inside the repository being triaged, holding that repository's `config/`, `data/` and `reports/`, with `bin/`, `themes/`, `docs/` and each prompt symlinked back here. Running the tool means running it from an install in the target repository; see [docs/install.md](docs/install.md). Optionally, install into this repository itself to triage our own backlog.

`bin/_install.py` draws the line the whole layout rests on:

- **`WORK_ROOT`**, from `os.path.abspath`, is the install: its `config/`, `data/` and `reports/`. Never `Path.resolve()` here, because resolving follows the `bin/` symlink an install is wired through and lands back in the checkout, which would send every ledger, batch and report to the wrong place.
- **`CODE_ROOT`**, from `Path.resolve()`, is this checkout: the program, and the defaults `bin/install-to` copies.

Every script imports `bin/_triage.py`, which requires an install and exits with what to do when there isn't one. `bin/install-to` imports only `bin/_install.py`, since it runs before an install exists. `tui/config.go` mirrors both (`FindInstallRoot`, `CodeRoot`).

## Invariants to keep

1. **Read-only against GitHub.** `gh issue view`, `gh pr view`, `gh pr diff`, `gh api ... issues`: GET requests. Nothing here labels, comments on, closes or merges. Anything that would is a separate, explicitly authorized piece of work that defaults to `--dry-run`; see `README.md`'s "Not built (yet)".
2. **Only the scripts write the data.** The TUI never opens `data/<owner>/<repo>/ledger.jsonl` or `groups/*.json` for writing: every save, approval, batch apply, group change and duplicate verdict shells out to `bin/apply`, `bin/group` or `bin/not-duplicate` through `runScript` in `tui/ghproc.go`. No new code path may break that.
3. **`gh` also is called from scripts.** Its GitHub reads go through `bin/fetch`, `bin/sync`, `bin/enrich-one`, `bin/batch` and `bin/group export`. Duplicate candidates come from `bin/similar`, which is offline.
4. **`bin/install-to` touches nothing outside the install** except a marked block in the target's `AGENTS.md` and one line in `.git/info/exclude`, both confirmed unless `--yes`. Its plan and its run are built by the same `installArgs`, so what the TUI shows and what happens cannot drift apart.
5. **The taxonomy is config, not code.** `config/taxonomy.json` (machine-checked) and `config/taxonomy.md` (the rationale) change together, and each install gets its own copy to own.
6. **Two stages, never merged.** A decision is _triaged_ when anyone (human or agent) proposes it and _reviewed_ only when a human confirms it. No code path may set `reviewed: true` by itself; that rule is the reason contributors trust the ledger.

## Working here

```sh
mise exec -- make format
mise exec -- make check # Go and Python tests, go vet, gopls, gofmt, Prettier
mise exec -- make build
mise exec -- make run ROOT=/path/to/a/repository/triage-o-mator
```

Tests build throwaway checkouts and installs in temporary directories with a fake `gh` (`tests/test_install.py` for the layout, `tests/test_fetch_similar.py` and `tests/test_agent_flow.py` for the scripts, `tui/*_test.go` for the TUI). None of them may touch a real install, a real repository or GitHub. Prettier formats the Markdown and the palette JSON, `gofmt` the Go; `make check` enforces both.

### House style

Two conventions `make check` couldn't enforce:

- **Comments are not hard-wrapped.** Write a comment as one long line and break it only at a logical boundary; a new thought, not column (80 or 100). The code here is written that way throughout, in Go, Python and Markdown alike.
- **Python and Go code breathes.** In `bin/` and `tests/`, separate the logical blocks inside a function with blank lines: after a guard clause, between setup, loop and output, around a multi-line statement. The Go in `tui/` reads well because of that spacing, and the scripts would be dense without it.

## Adding to it

- **A playbook:** write it in `prompts/`, add a row to `prompts/PLAYBOOK.md`'s "What you can be asked" table and to `README.md`'s prompts table, and give `bin/next` a suggestion that points at it.
- **A category or action:** `config/taxonomy.json` and `config/taxonomy.md` together. Installs keep their own copies, so this changes the default for new ones, not the ones already out there.
- **A script:** it imports `bin/_triage.py`, reads its paths from `WORK_ROOT`, and stays read-only against GitHub. Add it to `README.md`'s script table.
- **The playbook itself:** remember where it is read. `prompts/PLAYBOOK.md` is an install's `AGENTS.md`, so its paths and links are relative to an install, and `tests/test_install.py` walks every one of them from inside a fresh one.
