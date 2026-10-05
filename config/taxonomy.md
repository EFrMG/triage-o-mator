# Triage taxonomy

`taxonomy.json` holds `label_catalog`, the GitHub label definitions observed for the selected repository. Its `observed_at` time identifies the last successful online read; `pending` means no label definitions have been read for that repository. `guidance` is optional local advice, preserved by GitHub label ID when a label is renamed. Removed labels move to `retired` with their history intact. `label_catalog_archive` keeps prior repositories' catalogs if this install is pointed elsewhere and back. Running `bin/label-catalog sync` reads GitHub labels and updates this catalog without changing labels on GitHub. New decisions propose zero or more names from the observed catalog in `proposed_labels`; the existing `labels` field remains GitHub's observed item state. Proposed labels do not change GitHub by themselves.

`action_guidance` in the JSON holds descriptions for the current action names. `action_operations` maps each title to `comment`, `close`, `reopen`, or the explicit `none` choice. The TUI's Settings → Actions menu creates and edits action titles, descriptions, and operations through `bin/taxonomy-settings`; it does not apply an action to an item. Settings → Labels previews and explicitly changes GitHub label names and descriptions through `bin/label-definitions`, then refreshes the saved catalog. The older per-label local `guidance` is retained by label ID but is not edited in this menu.

Settings → Automations stores each writing action's `stage` or `execute` mode for the selected repository through `bin/action-policy`. Every type begins in `stage`; `none` has no write mode. Editing an action's title, operation or guidance expires its previous mode back to `stage` until a person explicitly configures it again. These settings do not select an action for an item or approve a ledger decision.

For PR closure types, `bin/auto-close propose --action ACTION` binds an exact proposal to the saved ledger action and retains the existing closure record. Issue actions and PR comments or reopenings use `bin/action-proposals`. `bin/action-pass preview --item KIND:NUMBER` checks selected proposals and repository policy offline, then `bin/action-pass run --item KIND:NUMBER --preview-sha256 HASH` stages them or uses the shared `comment-plus` writer when direct execution is enabled and selected target evidence is complete. Existing closure proposals without an action binding stay in the human approval path. A staged proposal, direct write outcome or uncertain attempt appears in Notifications; none changes ledger review.

A proposal with `--decision-question` always stages, even when its action type is set to `execute`. A person can answer the question in Notifications and approve the exact action without confirming the ledger decision. The attributed answer stays alongside the question, target, operation and comment in proposal history; it is local and is not posted as the GitHub comment. The action pass cannot execute a questioned proposal, including one that has been answered.

`issue_categories` and `pr_categories` are retained for existing ledger rows and older batch files. The decision form uses proposed labels and at most one suggested action; it does not assign a legacy category to new decisions. An action names a conversation or state write, independent of proposed labels. The `none` choice records that no conversation or state write is needed; a blank action means action assessment is still pending when labels are proposed, or no decision was made when no labels are proposed.

Item labeling is a separate pass controlled under Settings → Automations. It is ON by default for the selected repository and may be turned OFF there. An agent starts it explicitly; it never runs in the background. The pass acts on proposed labels from agent or human decisions regardless of the selected action or review status, after a bounded dry-run list and live revalidation. It records outcomes outside the ledger, removes only labels it previously added, and pauses items after human corrections or uncertain writes. A proposed label or a reviewed decision alone does not perform a GitHub write.

It is a living document. If a label or action stops being useful, or a new one is clearly needed, propose the change to a human maintainer rather than inventing it in a decision. GitHub owns label definitions; Settings reads and explicitly edits them. This install owns its action names and descriptions in `taxonomy.json`.

## Legacy issue categories

| Category            | Meaning                                                                                                                                                                                                                                                                           |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `bug`               | Reproducible defect in Omarchy itself.                                                                                                                                                                                                                                            |
| `hardware-specific` | Real bug, but tied to specific hardware/drivers (a Mac model, an Nvidia card, a laptop panel) rather than Omarchy generally. Worth tracking separately since fixes are narrower and often not actionable by the core team.                                                        |
| `support-question`  | Reporter needs help using/configuring their system; not a defect in Omarchy.                                                                                                                                                                                                      |
| `feature-request`   | Asking for new capability or a change to opinionated defaults.                                                                                                                                                                                                                    |
| `duplicate`         | Substantially the same as another open issue. Always name the issue it duplicates in `reason`.                                                                                                                                                                                    |
| `docs`              | Documentation is missing, wrong, or unclear.                                                                                                                                                                                                                                      |
| `needs-info`        | Can't be triaged further without a repro, logs, version info, etc. from the reporter.                                                                                                                                                                                             |
| `resolved`          | Nothing left to do here: a fix has shipped (in an Omarchy release, upstream, or a driver) or the reporter confirmed an answer or workaround settled it. Name the evidence in `reason`. A workaround Omarchy should still build in is not resolved; keep the item's real category. |
| `stale`             | Old, inactive, and either superseded or abandoned by the reporter (no response to a prior ask).                                                                                                                                                                                   |
| `out-of-scope`      | Conflicts with Omarchy's opinionated design, or belongs upstream (Arch, Hyprland, an app it ships) rather than in this repo.                                                                                                                                                      |
| `invalid`           | Spam, empty, or not a real issue.                                                                                                                                                                                                                                                 |

## Legacy PR categories

| Category                | Meaning                                                                                               |
| ----------------------- | ----------------------------------------------------------------------------------------------------- |
| `merge-ready`           | Small, focused, matches project conventions, looks safe to merge as-is.                               |
| `trivial`               | Typo/formatting/shellcheck-only or similarly low-risk. Still needs a merge decision, just a fast one. |
| `needs-revision`        | Good direction, but needs changes, tests, or cleanup first.                                           |
| `duplicate-pr`          | Overlaps with another open PR. Always name the other PR in `reason`.                                  |
| `out-of-scope`          | Personal preference or config that doesn't fit Omarchy's opinionated defaults.                        |
| `needs-maintainer-call` | Legitimate design/architecture decision that only a core maintainer should make.                      |
| `stale`                 | Inactive, has merge conflicts, or the author has gone quiet after review feedback.                    |
| `invalid`               | Spam or broken (doesn't apply, empty diff, etc.).                                                     |

## Actions

The action is the _recommended conversation or state operation_; independent of proposed labels, since items with the same labels can warrant different actions. A title can give a specific reason, while `action_operations` identifies the write. These are recommendations only: selecting or reviewing one never performs the write. `comment`, `close`, and `reopen` currently need an exact `comment-plus` preview and approval. Close and reopen always publish an explanatory comment first. The separate labeling pass reads proposed labels without consulting the action.

- `no-action-needed` (`none`): no supported conversation or state write is justified now; the labeling pass may still apply proposed labels.
- `comment-request-info` (`comment`): ask the reporter for repro, logs, or version details.
- `comment-feedback` (`comment`): leave a conversation comment with concrete feedback.
- `close-duplicate` (`close`): explain the duplicate and name the original.
- `close-stale` (`close`): explain why the item is inactive.
- `close-out-of-scope` (`close`): explain why the request is outside this repository's scope.
- `close-resolved` (`close`): point to the verified fix or answer that settled it.
- `close-with-explanation` (`close`): explain another specific reason for closure.
- `reopen-with-explanation` (`reopen`): explain why a closed item needs further work.

## Confidence

`low` / `medium` / `high`: how sure the triager is about the proposed labels and action. **Use `low` liberally.** A wrong `high`-confidence call that a human rubber-stamps is worse than an honest `low` that gets a second look. When no observed label fits, leave the proposal empty and explain the gap in `reason`. When human judgment is needed, state the specific question in `agent_notes` and choose the concrete operation that would follow that judgment, or `no-action-needed` if no conversation or state write is justified yet.

## What good triage looks like

1. Read the title, body, and at least the first couple of comments before deciding, not just the title.
2. Before marking something `duplicate`/`duplicate-pr`, actually check the issue you think it duplicates still exists and is genuinely the same report, not just a similar symptom.
3. Prefer specific `reason` text a human can skim in three seconds over vague restatements of a label name. "Same freeze as #12201, same GPU" beats "duplicate of another issue."
4. Never invent a label or action. `bin/apply` refuses proposed labels absent from this repository's observed catalog, and warns about unrecognized legacy categories or actions.
5. Leaving `reviewed: false` is the default and correct state for anything an agent triaged. Only a human reviewer flips it to `true` (see AGENTS.md).
