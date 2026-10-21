# Size of PRs

Settings → **Size of PRs** toggles deterministic GitHub labeling for the selected repository. It defaults **OFF**. **ON** authorizes creation/color updates for the four size label definitions and automatic size label updates after each TUI corpus refresh. No agent pass is involved. Existing size labels remain visible when the setting is OFF.

Size counts all additions plus deletions and all changed files, including generated files and lockfiles as reported by GitHub. Both limits must fit a category; exceeding either raises the size.

| Label    | Changed lines        | Changed files        | GitHub color           |
| -------- | -------------------- | -------------------- | ---------------------- |
| Size: S  | ≤50                  | ≤3                   | Light green `90EE90`   |
| Size: M  | ≤250                 | ≤10                  | Yellow-green `9ACD32`  |
| Size: L  | ≤1,000               | ≤25                  | Orange-red `FF4500`    |
| Size: XL | Above either L limit | Above either L limit | Brilliant red `FF0000` |

Exact size labels appear before Score on item cards and the item header. Only the size letter is colored: S is green, M yellow, and L/XL red. Only the score fraction is colored with the active theme's error color for 1–2, warning color for 3, and success color for 4–5. If multiple sizes are observed, the largest is displayed until the next successful size pass replaces them with one.

The size pass refreshes the selected repository's label catalog after verifying the size definitions, so the item form can offer them without a separate catalog refresh.

The pass uses the frozen corpus's selected summary snapshots. It verifies repository/item identity, payload checksums, completeness and freshness before reading counts. Missing or unverifiable summaries are skipped. Before publication it reads the live PR and requires the cached identity, base/head commits and counts to match an open PR. It adds the selected size and removes only the other three exact size labels; unrelated labels, local decisions, label proposals and Pending review requests are preserved. Each write rechecks the setting and PR revision. CLI corpus acquisition remains read-only against GitHub; the TUI invokes the separate size writer after acquisition.

For CLI use, first inspect the policy and save the selected repository setting using its returned revision:

```sh
bin/pr-size status --expected-repo OWNER/REPO
bin/pr-size set-enabled --expected-repo OWNER/REPO --enabled on --expected-revision REVISION
bin/pr-size run --expected-repo OWNER/REPO --corpus CORPUS_ID
bin/pr-size run --expected-repo OWNER/REPO --corpus CORPUS_ID --apply --request-budget 100
```

Without `--apply`, a run previews cached size proposals offline and makes no GitHub calls. Publication requires the setting to be ON. Request budgets include reads and writes; the TUI allocates a finite allowance based on corpus membership. Skips and outcomes are returned as JSON and saved in `data/OWNER/REPO/local/pr-size-outcomes.jsonl`. The TUI notices script-owned ledger label updates through its ordinary reload mechanism.

An attempted write is recorded before contacting GitHub. Interrupted or unverifiable writes block subsequent size passes, including automatic retries. `status` and the Settings page show pending writes. Inspect the affected GitHub PR or label definition, then explicitly clear the block:

```sh
bin/pr-size reset --expected-repo OWNER/REPO
bin/pr-size reset --expected-repo OWNER/REPO --apply
```

Reset clears only the local uncertainty block. The next pass reads GitHub again before making changes. Disable the setting to stop future automatic sizing.
