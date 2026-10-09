# Playbook evaluations

This directory is the shareable kit for testing triage-o-mator's playbooks: the method and the seed and run scripts. It ships no run data. **Start with the [current plan](plan.md).** It contains the assessment method, per-playbook case matrix, agent task form, run sequence, one-report format and archive/deletion rules. [Seed mechanics](seed/README.md) and the run scripts are supporting instructions, not competing plans.

## What is here and what is not

| Here, shared                                                                | Local only, under `DOCS/triage-playbook-evals/`                                     |
| --------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| `plan.md`, `run-eval.sh`, `archive-run.sh`                                  | `seed/runs/RUN_ID/`: previews, audits, handoffs, agent notes, task text, the report |
| `seed/*.py`, `seed/fixtures.json`                                           | `seed/RUN_ID.json` and `seed/runs/RUN_ID/gold.json` of a run that is not over       |
| optionally, a finished run's `seed/RUN_ID.json` and `seed/RUN_ID.gold.json` | `archives/`: verified captures of the private repository and installs               |

Run records and archives stay local because they mirror a private repository, hold account-level API replies and absolute paths, and are large. Set `TRIAGE_EVAL_RECORDS` to keep them somewhere else if need be. A run's result is shared as its single `report.md`, posted with the archive's SHA-256 so the record can be produced on request.

A fixture and its answer guide may be copied here **after** their run is archived, so others can rerun it. Once published, those cases are no longer held-out controls; write fresh per-run cases for the next run.

## Reproducing a run

You need a GitHub account to create a private repository, an authenticated `gh`, and a clean lazygit checkout at the `base_sha` in `seed/fixtures.json`.

```sh
export TRIAGE_EVAL_REPOSITORY=YOUR_ACCOUNT/lazygit-test # the private fixture repository to create
export TRIAGE_EVAL_SOURCE_CLONE=/path/to/lazygit # pinned public checkout
export TRIAGE_EVAL_CLONE_PATH=/path/to/lazygit-test # where the private clone will go
python3 seed/selftest.py # offline checks, no GitHub
```

Then follow [the plan](plan.md) from read-only preview through exact seed approval, per-agent installs, frozen agent tasks, one `report.md`, verified archive and separately approved repository deletion. Agents get their own directory under `/tmp` with a copy of the generated `agent-handoff.md` and their task text, never a path into this kit or the records.

**A rerun is not a replay.** The fixture, tasks and evidence can be held fixed; agent answers cannot. Expect the same cases and a comparable picture, not identical judgments. The public half reads a frozen lazygit corpus that the seed does not create: acquire your own with the install's normal scripts and name it in the fixture's `public_read_scope`.
