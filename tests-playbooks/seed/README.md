# Private fixture mechanics

The [current evaluation plan](../plan.md) owns the assessment method and run order. This file explains only the seed. `seed.py` previews and, after review of an exact plan hash, creates a private `someonesalter/lazygit-clone` repository and a local install. It does not make a playbook judgment. Verify the current remote state at the start of every run.

`fixtures.json` pins the repository, public base commit and ten topic templates. For a new run ID, create and review `RUN_ID.json` in the local records' `seed/` directory (`DOCS/triage-playbook-evals/seed/` unless `TRIAGE_EVAL_RECORDS` says otherwise), using the schema below. The seed looks there first and then here, where a finished run's fixture can be published. Add `public_read_scope` with `repository`, `corpus_id`, `inventory_snapshot`, `source_commit` and `mode: fixed-snapshot offline`, plus `briefing_batches` with `issue` and `pr` size lists. Save the withheld guide separately as `runs/RUN_ID/gold.json` in the records; the freeze refuses a new run without it. The seed refuses an ID with no fixture file or a missing scope. The one exception is run ID `0`, which seeds the base topic templates alone as a setup smoke check and gets no public scope or agent installs; the batch sizes must cover the selected counts and each must be at most 40. The current schema adds three issue controls and one edited-file PR per topic to the base 90 issues and 20 PRs, producing 120 issues and 30 PRs. Titles and body text are synthetic claims, not verified lazygit bugs. Case roles appear in `seed.py`, the preview, `seed-state.json` and the hand-written withheld guide, never in remote-visible text. Agents receive a concrete launch context and one task text, with no evaluator handoff or answer-role hints.

The base and per-topic expansions produce 120 issues and 30 PRs. Optional `extra_issues` and `extra_prs` lists add up to 99 items of each kind; each item needs a unique `id`, `title` and `body`. Supplemental PRs also need the file edits described below. For example, an extra issue can be `{"id": "config-followup", "title": "…", "body": "…"}`. These additions are included in preview hashes, seeding, auditing and freeze counts; the `briefing_batches` totals must cover the actual expanded counts. The next planned fixture uses 180 issues and 45 PRs.

Multi-file PRs use `files`, a list of one to twenty edits of existing pinned source files. Every edit is bound into the preview, created in one commit and audited against the exact changed-file set and base/result contents. The original one-file form below remains supported. Supplemental items and edits receive opaque public IDs; evaluator case IDs remain local.

A run fixture has one entry per topic `id` in `fixtures.json` under both `issues` and `prs`:

```json
{
  "issues": {
    "TOPIC_ID": {
      "other_words_title": "same report as the topic's clear bug, different title and wording",
      "other_words_body": "…",
      "different_cause_title": "same title as other_words, different cause",
      "different_cause_body": "…",
      "unresolved_title": "overlaps, with one discriminating detail missing",
      "unresolved_body": "…"
    }
  },
  "prs": {
    "TOPIC_ID": {
      "title": "…",
      "body": "…",
      "path": "file in the pinned checkout",
      "append": "text added at the end"
    }
  },
  "comments": [
    {
      "item_id": "TOPIC_ID-alt_duplicate",
      "body": "one follow-up comment on that issue"
    }
  ],
  "public_read_scope": {
    "repository": "…",
    "corpus_id": "…",
    "inventory_snapshot": "…",
    "source_commit": "…",
    "mode": "fixed-snapshot offline"
  },
  "briefing_batches": { "issue": [40, 40, 40], "pr": [30] }
}
```

A run-specific PR edits existing files: use `append`, or `"replace": { "old": "text that occurs once", "new": "…" }`. For multiple files, replace the top-level `path`/`append`/`replace` fields with `"files": [{"path": "…", "append": "…"}, {"path": "…", "replace": {"old": "…", "new": "…"}}]`. Paths must be unique and repository-relative, and every edit must change its file. Do not mix the two forms. Base template PRs still add one note file.

`comments` may be empty; an `item_id` names a seeded item and each item takes at most one follow-up. Comments default to issues; set `"kind": "pr"` for a PR follow-up. Supplemental item IDs can also be used. Unknown, duplicate or empty follow-ups are rejected before seeding. The `already_answered` issue of every topic already gets one from the seed.

Set `TRIAGE_EVAL_SOURCE_CLONE` and `TRIAGE_EVAL_CLONE_PATH` if the pinned public checkout or disposable private clone differs from the defaults `/workspace/lazygit` and `/workspace/lazygit-clone`. The authenticated owner in `fixtures.json` must match the private repository; set `TRIAGE_EVAL_REPOSITORY=OWNER/REPO` to seed under another account without editing the file. The preview binds the clone path, fixture content and previous archive into its hash. It checks the pinned source, repository identity and existing seed progress before writing anything. On apply, it disables Actions, removes Dependabot configuration, creates issues and PR branches, installs triage-o-mator, fetches the initial inventory and syncs a clean ledger. An interrupted or uncertain GitHub write needs inspection; the script does not blindly replay it.

Use [`run-eval.sh`](../run-eval.sh) from the kit root for `plan`, `seed` and `test-start`. `plan-after-reset RUN_ID PREVIOUS_ARCHIVE` is a read-only prospective preview while the previous remote still exists; it checks the live remote and local clone against the verified archive. After separately approved deletion, `plan RUN_ID PREVIOUS_ARCHIVE` must produce the same hash. `test-start` audits the newly seeded fixture **before** playbook writes and generates evaluator-only `freeze.json` (seeded keys, plan and guide hashes, playbook hashes, briefing sizes). `agent-install RUN_ID AGENT private|public` then builds one lane-specific clone and install outside the kit, and refuses if the seeded install has changed since `test-start`. Freeze the actual judgment case snapshots and one task per playbook as directed by the plan; the evaluator freeze alone is not an assessment.

Earlier runs' seed plans, fixtures, audits, answers and results are kept in the local records, not in this kit. During a blind pass, use the `launch` object printed by `agent-install` and saved in `agent-installs.json`: it contains the concrete absolute working directory, the installed `AGENTS.md` path and a routing message. The command checks that `config/repo` selects the assigned repository and that `AGENTS.md` links to the triage playbook before publishing this context. Set the runtime's working directory when supported; otherwise supply the routing message before the task. Do not put path placeholders or agent-name substitution instructions in task text. Both attempts receive the same task text; only their launch paths differ. Capture command logs in the evaluator's runtime, without agent-written logging wrappers. Everything else in this kit is withheld, including `seed-state.json` and the run directory itself.
