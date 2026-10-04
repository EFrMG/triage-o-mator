# Triage taxonomy

`taxonomy.json` holds `label_catalog`, the GitHub label definitions observed for the selected repository. Its `observed_at` time identifies the last successful online read; `pending` means no label definitions have been read for that repository. `guidance` is optional local advice, preserved by GitHub label ID when a label is renamed. Removed labels move to `retired` with their history intact. `label_catalog_archive` keeps prior repositories' catalogs if this install is pointed elsewhere and back. Running `bin/label-catalog sync` reads GitHub labels and updates this catalog without changing labels on GitHub. New decisions propose zero or more names from the observed catalog in `proposed_labels`; the existing `labels` field remains GitHub's observed item state. Proposed labels do not change GitHub by themselves.

`action_guidance` in the JSON holds descriptions for the current action names. The TUI's Settings → Actions menu creates and edits action titles and descriptions through `bin/taxonomy-settings`; it does not apply an action to an item. Settings → Labels previews and explicitly changes GitHub label names and descriptions through `bin/label-definitions`, then refreshes the saved catalog. The older per-label local `guidance` is retained by label ID but is not edited in this menu.

`issue_categories` and `pr_categories` are retained for existing ledger rows and older batch files. The decision form uses proposed labels and actions; it does not assign a legacy category to new decisions. Actions remain the recommended next step, independent of labels.

Item labeling is a separate pass under Settings. It is disabled until a person enables it for this repository, then acts only on reviewed decisions after a bounded preview and live revalidation. It records outcomes outside the ledger, removes only labels it previously added, and pauses items after human corrections or uncertain writes. A proposed label or a reviewed decision alone does not perform a GitHub write.

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

The action is the _recommended next step_; independent of proposed labels, since items with the same labels can warrant different actions.

- `label-only`: apply/confirm a label, no other action needed yet.
- `comment-request-info`: ask the reporter for repro/logs/version.
- `comment-explain-close`: explain the reasoning and close.
- `close-duplicate`: close, pointing at the original.
- `close-stale`: close as inactive.
- `close-out-of-scope`: close, explaining why it's out of scope.
- `close-resolved`: close, pointing at the fix or answer that settled it.
- `approve-merge-candidate`: flag for a maintainer to merge.
- `request-changes`: leave review feedback on a PR.
- `escalate-maintainer`: needs a human judgment call before anything else happens (design decisions, ambiguous scope, anything sensitive).
- `no-action-needed`: already in the right state (e.g. already labeled and waiting).

## Confidence

`low` / `medium` / `high`: how sure the triager is about the proposed labels and action. **Use `low` liberally.** A wrong `high`-confidence call that a human rubber-stamps is worse than an honest `low` that gets a second look. When no observed label fits, leave the proposal empty, explain the gap in `reason`, or use `escalate-maintainer` when a specific human judgment is needed.

## What good triage looks like

1. Read the title, body, and at least the first couple of comments before deciding, not just the title.
2. Before marking something `duplicate`/`duplicate-pr`, actually check the issue you think it duplicates still exists and is genuinely the same report, not just a similar symptom.
3. Prefer specific `reason` text a human can skim in three seconds over vague restatements of a label name. "Same freeze as #12201, same GPU" beats "duplicate of another issue."
4. Never invent a label or action. `bin/apply` refuses proposed labels absent from this repository's observed catalog, and warns about unrecognized legacy categories or actions.
5. Leaving `reviewed: false` is the default and correct state for anything an agent triaged. Only a human reviewer flips it to `true` (see AGENTS.md).
