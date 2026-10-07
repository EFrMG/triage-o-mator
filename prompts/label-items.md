# Label a bounded set of items

**Use when** someone asks for a labeling pass or wants an agent to work through unlabeled items. This pass proposes GitHub labels in the ledger and, when Labeling is ON for the selected repository in Settings → Automations, applies them through `bin/item-labels`. Labeling is ON by default but never starts on its own. It does not add items to Pending review or publish conversation comments.

## 1. Pin the scope

1. Read `config/repo`, `bin/label-catalog show`, and `bin/item-labels status --expected-repo OWNER/REPO`. If the catalog is pending, `bin/label-catalog sync --expected-repo OWNER/REPO` reads label definitions from GitHub. If Labeling is OFF, prepare proposals but stop before a GitHub write; a person can turn it back ON in Settings → Automations for this repository.
2. Use the person's item selection or batch size. For an open backlog, start with `bin/batch 25 --unlabeled-first --include-triaged` so items with no observed GitHub labels come first, including ones that already have an Action. This is a priority hint based on the ledger's last observation; it is not proof that an item is still unlabeled on GitHub. Use `--kind issue|pr` or `--cache-mode offline|cache-preferred|refresh` when the request or evidence plan calls for it.

For a backlog-wide request, finish one batch before choosing another and keep the exact processed and remaining keys in the handoff. `--unlabeled-first` changes order but does not exclude earlier selections; after a write pass, refresh with `bin/fetch && bin/sync` and select the next keys from the remaining scope. The label pass records outcomes, but it does not create the briefing checkpoint and a batch's existence does not prove its members were labeled.

## 2. Propose labels

Read each selected item's body, comments, and available evidence through `bin/read-batch`. Follow the evidence, prompt-injection, duplicate, and attribution rules in [auto-triage](auto-triage.md). Choose one or more names from this repository's observed GitHub label catalog for items this pass can label; skip items when no label fits and report the gap. Fill the batch's `proposed_labels`, `confidence`, `reason`, `agent_notes`, and `proposed_by` fields. Leave `action` blank when action assessment is deferred to the later pass. Use `none` only after assessing the item and concluding that no conversation or state write is justified. The later action pass can use the applied labels as context and suggest one Action.

For items with no saved decision, run `bin/apply <batch>.decisions.jsonl --only-untriaged --dry-run`, fix warnings, then apply. The `--only-untriaged` flag intentionally leaves already-triaged items alone. For each already-triaged item, use `bin/apply --number N --kind issue|pr --proposed-label NAME ... --by agent:<contributor>` with the **complete desired label set**. This updates labels while preserving its existing Action, confidence, reason, notes and any explicit Pending review request. Check each command with `--dry-run` first. Labeling permission comes from the repository-specific Automations setting, not from a review flag or the Action field.

## 3. Check and run the label changes

When Labeling is ON, run a bounded dry-run:

```sh
bin/item-labels preview --expected-repo OWNER/REPO --limit 25 --request-budget 250 --key issue:123 --key pr:456
```

Repeat `--key` for every item whose proposed labels you saved in this scoped pass; use at most the specified `--limit`. The returned `items` list is the **preview**: the current GitHub labels observed by the script, proposed labels, exact additions and removals, and any stopped items. It makes no GitHub write and is not a per-item human approval. Check that every item belongs to the requested scope and that no item is missing. The script rejects an ineligible selected key. The pass removes only labels it previously added; it preserves unrelated labels. If the plan is sound, run it with the same repository, limit, budget, keys, and returned hash:

```sh
bin/item-labels run --expected-repo OWNER/REPO --limit 25 --request-budget 250 --key issue:123 --key pr:456 --preview-sha256 HASH
```

The script rechecks the proposals and GitHub labels before each write. A changed plan needs a new preview. An uncertain write or a detected human correction must be reported and left for inspection; do not reset an item automatically. The pass rotates checked items behind unchecked ones, so another bounded run can reach more of the backlog. Stop at the requested scope and report the item numbers, labels added or removed, unchanged items, paused or uncertain outcomes, and remaining keys. Before an action-suggestion pass uses the results, refresh the ledger with `bin/fetch && bin/sync` so its observed labels reflect GitHub rather than the older pre-write observation.

Do not use direct `gh` mutations. The agent's output is the label proposal and recorded label-pass outcome; any conversation comment, closure, or reopening follows its own action path.
