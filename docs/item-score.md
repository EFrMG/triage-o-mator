# Item Score

Item Score is a local, source-bound 0–5 assessment of an issue's clarity and actionability or a PR's correctness, safeguards and reviewability. It is separate from the item's priority, triage confidence, evidence coverage, human review and approval of an exact GitHub write. A high score never means a PR may be merged or an issue may be closed.

The [scoring playbook](../prompts/score-items.md) gives the point anchors. Issue points are clarity (0–2), support (0–2) and actionability (0–1). PR points are correctness (0–2), safeguards (0–2) and reviewability (0–1). The score's reason must explain each mark, the strongest concern and any gap. A suggestion names one next check or decision; it does not change the ledger action.

For a number, `bin/item-score` verifies one immutable snapshot and its objects offline. It requires a complete issue summary with a body, or a complete PR summary, files and diff. It refuses a snapshot whose item revision differs from the ledger. Other partial components stay visible as gaps in the reason. If the core evidence is unavailable, record **unassessed** with a reason; missing evidence is not a zero. A later ledger `updated_at` makes the old number appear stale until a new source-bound assessment is saved. That comparison detects a changed recorded item revision, not every possible change on GitHub before the next sync.

```sh
bin/item-score --expected-repo OWNER/REPO set --kind issue --number N --snapshot SNAPSHOT \
  --clarity 2 --support 1 --actionability 1 --reason '...' --suggestion '...' --by 'agent:NAME'
bin/item-score --expected-repo OWNER/REPO set --kind pr --number N --snapshot SNAPSHOT \
  --correctness 1 --safeguards 1 --reviewability 1 --reason '...' --suggestion '...' --by 'agent:NAME'
bin/item-score --expected-repo OWNER/REPO set --kind issue --number N --unassessed \
  --reason 'Selected body unavailable' --by 'agent:NAME'
bin/item-score --expected-repo OWNER/REPO show --kind issue --number N
```

The score is stored as its own `item_score` object in the ledger, with rubric version, dimension marks, reason, suggestion, snapshot ID, source revision and assessor. It does not alter `action`, `confidence`, `reviewed` or action proposals. The TUI shows **Score —** for unassessed items, colors 0–2 red, 3 yellow and 4–5 green, and exposes the reason in the item panel. Settings → Automations → Scoring copies a bounded local pass prompt; it is not a background process or a GitHub automation setting.
