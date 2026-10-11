# Playbook evaluations

This directory is the shareable kit for testing triage-o-mator's playbooks: the method and the seed and run scripts. It ships no run data. **Start with the [current plan](plan.md).** It contains the assessment method, per-playbook case matrix, agent task form, run sequence, one-report format and archive/deletion rules. [Seed mechanics](seed/README.md) and the run scripts are supporting instructions, not competing plans.

## What is here and what is not

| Here, shared                                                                | Local only, under `DOCS/triage-playbook-evals/`                                             |
| --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| `plan.md`, `run-eval.sh`, `archive-run.sh`                                  | `seed/runs/RUN_ID/`: previews, audits, evaluator freeze, agent notes, task text, the report |
| `seed/*.py`, `seed/fixtures.json`                                           | `seed/RUN_ID.json` and `seed/runs/RUN_ID/gold.json` of a run that is not over               |
| optionally, a finished run's `seed/RUN_ID.json` and `seed/RUN_ID.gold.json` | `archives/`: verified captures of the private repository and installs                       |

Run records and archives stay local because they mirror a private repository, hold account-level API replies and absolute paths, and are large. Set `TRIAGE_EVAL_RECORDS` to keep them somewhere else if need be. A run's result is shared as its single `report.md`, posted with the archive's SHA-256 so the record can be produced on request.

A fixture and its answer guide may be copied here **after** their run is archived, so others can rerun it. Once published, those cases are no longer held-out controls; write fresh per-run cases for the next run.

## Reproducing a run

You need a GitHub account to create a private repository, an authenticated `gh`, and a clean lazygit checkout at the `base_sha` in `seed/fixtures.json`.

```sh
export TRIAGE_EVAL_REPOSITORY=YOUR_ACCOUNT/lazygit-clone # the private fixture repository to create
export TRIAGE_EVAL_SOURCE_CLONE=/path/to/lazygit # pinned public checkout
export TRIAGE_EVAL_CLONE_PATH=/path/to/lazygit-clone # where the private clone will go
python3 seed/selftest.py # offline checks, no GitHub
```

Then follow [the plan](plan.md) from read-only preview through recorded authorization, lane-specific agent installs, frozen tasks, one `report.md`, verified archive and authorized repository deletion. A person can authorize a named private fixture and bounded seeding, preparation, write-test, policy and post-verification deletion operations once for the run; retain exact plans, hashes and receipts without repeatedly asking permission. Operations outside that scope still need authorization. Recoverable subagent command errors are recorded without pausing unrelated tasks; uncertain writes and evidence/integrity failures stop the affected operation. Each fresh agent receives its concrete install launch context under `/tmp` and the frozen task text, never a path into this kit or the records. Set its working directory explicitly when the runtime supports it; otherwise use the concrete routing message emitted by `agent-install`. The evaluator captures tool-call logs; agents do not build logging wrappers.

**A rerun is not a replay.** The fixture, tasks and evidence can be held fixed; agent answers cannot. Expect the same cases and a comparable picture, not identical judgments. The public half reads a frozen lazygit corpus that the seed does not create: acquire your own with the install's normal scripts and name it in the fixture's `public_read_scope`.

## Deliver the complete playbook to subagents

Before task work, have each subagent read the installed main playbook with a standalone command from its assigned install:

```sh
cat AGENTS.md
```

Then `cat` the selected task playbook separately. Give the tool enough output room for the whole document. The evaluator checks that the complete text reached the agent, as described in [the plan](plan.md#assign-agents-and-bounded-playbook-tasks).

## Keep focused runs proportionate

Declare only the cases and contrasts needed for the question, reuse verified frozen evidence and skip remote seeding and live writes when the task is entirely offline. Keep exact inputs in one preparation manifest, one seal per attempt, one adjudicated report and one verified archive. Keep required source/artifact hashes in those records and the archive manifest, plus an external archive checksum; ordinary working notes and editable publication drafts do not each need a checksum sibling. See [the plan](plan.md#retention-for-focused-runs) for the retained boundaries and existing retirement contract.

After each agent stops and its output is sealed, run `run-eval.sh agent-retire RUN_ID AGENT`. It verifies the seal, saved install files, registered paths and clean pinned checkout before removing the parent checkout. The complete install stays at its registered path for continuation transfers and archiving. Retirement receipts live beside the sealed output; interrupted cleanup can resume with the same command. Never retire a running agent’s checkout or remove retained installs before archive verification. See [the plan](plan.md) for the full procedure.
