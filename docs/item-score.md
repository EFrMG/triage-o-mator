# Item Score

Item Score is a local, source-bound 0–5 assessment of an issue's clarity and actionability or a PR's correctness, safeguards and reviewability. It is separate from the item's priority, triage confidence, evidence coverage, explicit Pending review and approval of an exact GitHub write. A high score never means a PR may be merged or an issue may be closed.

The [scoring playbook](../prompts/score-items.md) gives the point anchors. Issue points are clarity (0–2), support (0–2) and actionability (0–1). PR points are correctness (0–2), safeguards (0–2) and reviewability (0–1). The score's reason must explain each mark, the strongest concern and any gap. A suggestion names one next check or decision; it does not change the ledger action.

For a number, `bin/item-score` verifies one immutable snapshot and its objects offline. It requires a complete issue summary with a body, or a complete PR summary, files and diff. It refuses a snapshot whose item revision differs from the ledger. Other partial components stay visible as gaps in the reason. If the core evidence is unavailable, record **unassessed** with a reason; missing evidence is not a zero. Supply `--snapshot` when selected evidence exists, even if incomplete or missing a body, to retain its verified revision and coverage. Without a snapshot, the unassessed result binds the ledger's current observed revision and has no verified evidence reference. An unassessed result also returns to the scoring queue once a newer snapshot of the item is saved with its core evidence complete: the summary for an issue, or the summary, files and diff for a PR. `bin/next` counts it again and `bin/item-score show` reports `new_evidence`. That check reads the current-item index as a lead and never fetches. Either result otherwise stays current until what it examined changes: a new head commit for a PR, a different comment count for an issue. Labels, title or description edits, new comments on a PR and pushes to a PR's base branch do not return an item to the scoring queue. GitHub's issue list carries no head commit, so `bin/fetch` makes one extra read-only request for each scored open PR whose update time moved, and `bin/sync` saves that head in the ledger. When a read fails, or a result was saved before these bindings existed, the recorded update time decides instead. The comparison uses what the last sync observed, not every change on GitHub since.

```sh
bin/item-score --expected-repo OWNER/REPO set --kind issue --number N --snapshot SNAPSHOT \
  --clarity 2 --support 1 --actionability 1 \
  --reason 'Clarity 2: Trigger and expected result are clear. Support 1: One useful example is given. Actionability 1: A focused next check is possible.' \
  --suggestion '...' --by 'agent:NAME'
bin/item-score --expected-repo OWNER/REPO set --kind pr --number N --snapshot SNAPSHOT \
  --correctness 1 --safeguards 1 --reviewability 1 \
  --reason 'Correctness 1: The approach is plausible with one unresolved case. Safeguards 1: A partial check is visible. Reviewability 1: The change is focused.' \
  --suggestion '...' --by 'agent:NAME'
bin/item-score --expected-repo OWNER/REPO set --kind issue --number N --unassessed --snapshot SNAPSHOT \
  --reason 'Selected body unavailable' --by 'agent:NAME'
bin/item-score --expected-repo OWNER/REPO show --kind issue --number N
```

The score is stored as its own `item_score` object in the ledger, with rubric version, dimension marks, reason, suggestion, snapshot ID, source revision and assessor. `bin/item-score show` reports `current` for a numeric score and `assessment_current` for either a numeric or unassessed result at the ledger revision; an older unbound unassessed record is due for another pass. The record does not alter `action`, `confidence`, an explicit Pending review request or action proposals. The TUI aligns the score at the right of item card and item view titles, shows **Score —** for unassessed items, and colors 0–2 red, 3 yellow and 4–5 green. A number whose item has since changed stays visible in its color as **Score 3/5 (stale?)** until it is scored again. The item form shows the assessor and three separate dimension explanations; the full saved reason and source remain available through `bin/item-score show` and copied item context. Settings → Automations → Scoring copies a bounded local pass prompt; it is not a background process or a GitHub automation setting.
