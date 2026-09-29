# Comment-plus: publishing comments and changing item state

The [TUI tutorial](tutorial.md#12-hand-reviewed-work-to-maintainers) covers the controls for comments, closures and reopenings. The composer keeps the target URL visible; its optional Markdown preview renders locally, without contacting GitHub. An external editor pauses the TUI on a private temporary file and returns to the preview. Nothing is published until the exact target, text and any state change are approved.

A comment on an issue or PR is a conversation comment, not an inline review. An explained closure publishes the comment first, then closes the item; the script checks for text but cannot judge the explanation. A merged PR cannot be reopened. Bulk reopening uses one shared comment but a separate exact plan and outcome for each target; it stops on the first uncertain outcome. After publication, the TUI refreshes the discussion and ledger, and the displayed state updates after the direct GitHub item read is synced.

`EDITOR` may include arguments, such as `nvim` or `code --wait`; the command must wait until editing is finished. If unset, it defaults to `vi`. Imported CRLF newlines become LF and tabs expand to four spaces. Successful imports remove the temporary file; an editor or import failure keeps the current TUI draft and reports the temporary file's path for recovery.

From an install, save the proposed text in a UTF-8 file and preview it:

```sh
bin/comment-plus --expected-repo OWNER/REPO --kind issue --number 123 --body-file /tmp/comment.md
```

The default is an offline dry-run: it prints the target, exact body, `state_change: "none"`, a request ID and an approval hash without saving a proposal or contacting GitHub. `--dry-run` is also accepted. For GitHub Enterprise, include `--host HOSTNAME` in both invocations; the default is `github.com`, independent of `GH_HOST`.

After a human approves that exact plan, repeat the command with the printed values:

```sh
bin/comment-plus --expected-repo OWNER/REPO --kind issue --number 123 --body-file /tmp/comment.md \
  --publish --request-id REQUEST_ID --approve APPROVAL_HASH
```

Use `--kind pr` for a PR. `--body TEXT` is an alternative to `--body-file`. Changing the host, repository, kind, number, body or request ID invalidates the approval hash. The hash binds the plan; it does not authenticate a human or replace their approval. An agent must obtain the human's explicit approval of the target and text before publishing.

For a closure, add `--close` to both commands. The dry-run plan then shows `operation: "close"` and `state_change: "closed"`. Approval covers the comment and state change together. Publication checks that the live item is still open, posts the comment, and sends a `PATCH` with `state: "closed"` to the issues endpoint for an issue or the pulls endpoint for a PR. A changed or already closed target stops before the comment. A failed close after a successful comment leaves the comment published and records the close outcome as unknown.

For reopening, add `--reopen` to both commands instead. The plan shows `operation: "reopen"` and `state_change: "open"`. Publication checks that the live item is closed and, for a PR, that it is not merged before posting. It then comments and sends a `PATCH` with `state: "open"`. A failed reopen after a successful comment leaves the comment published and the state outcome unknown. For a TUI bulk reopening, each item has its own request ID and saved outcome; successful earlier items are not repeated after a later failure.

Publishing verifies the live target, then records the attempt in `data/<owner>/<repo>/writes/<request-id>.json` before sending the comment. Comment and state-change outcomes are separate; comment-only requests record `not_requested` for the state change. These records are local replay-protection and recovery state, owned by `bin/comment-plus` and Git-ignored even in adopted installs. The request ID and approval hash prevent a repeated request from posting again; the attempted text, target and outcome let you inspect an interrupted request. They are separate from evidence and ledger review status. Deleting a record removes replay protection for its request ID. Publishing never marks a decision reviewed or changes cached evidence.

Repeating a successful request returns its saved result without another write. An interrupted request or failed POST or PATCH has an `unknown` outcome: GitHub might already have accepted it. The same request is never automatically retried. Inspect GitHub and the saved record before preparing and approving a new request; a new request ID can post the same text again. Replay protection applies within this install; it is not a GitHub idempotency guarantee.

The implementation uses GitHub's [conversation comment endpoint](https://docs.github.com/en/rest/issues/comments#create-an-issue-comment), shared by issues and PRs, and the [issue](https://docs.github.com/en/rest/issues/issues#update-an-issue) or [PR](https://docs.github.com/en/rest/pulls/pulls#update-a-pull-request) update endpoint for state changes. Automated tests use fake `gh` in disposable installs.

`bin/install-to` maintains the ignore rule. Rerun the installer to apply it to an existing install. Ignore rules do not untrack records already added to Git or remove them from past commits.

## Pending PR closure proposals

The [PR closure playbook](../prompts/recommend-auto-close.md) saves a proposed rationale and exact comment through `bin/auto-close propose`. Proposal records in `data/<owner>/<repo>/auto-close/` are Git-tracked; local viewed and dismissed flags are ignored. Saving or viewing a proposal never writes to GitHub or marks a ledger decision reviewed.

`bin/auto-close --expected-repo OWNER/REPO inspect --host HOSTNAME --number N` performs two read-only REST GETs. It returns the PR head SHA and the issue endpoint's `updated_at`, which `bin/comment-plus` checks again before publishing a proposed closure. The read does not create a proposal or grant approval.

An exact `--replace-checkpoint` can update a pending proposal before any write attempt. The previous version remains in the tracked record and the updated proposal appears as new attention, even if the old version was dismissed. Proposals with an uncertain or completed write must be reconciled, not replaced.

To reject one pending proposal with shared feedback, use its exact checkpoint from `bin/auto-close --expected-repo OWNER/REPO list`:

```sh
bin/auto-close --expected-repo OWNER/REPO reject --number N --checkpoint CHECKPOINT --by NAME --reason 'Why this proposal should not proceed'
```

The tracked proposal keeps the earlier version and records who rejected it, when and why. Rejection refuses stale checkpoints and any saved GitHub write attempt; it makes that version ineligible for review or execution without changing the ledger or GitHub. A rejected proposal by itself appears under **Past actions** in Notifications with the reason available to read; other activity on the same item can still put its combined card under **Needs attention**. A previous local dismissal does not hide the newly saved rejection; dismiss it separately if desired. Dismissal changes only local presentation, and reconsideration of a rejected proposal is a later, explicit workflow.

Notifications shows actionable proposals first. The [tutorial](tutorial.md#11-track-follow-up-activity) explains how to inspect, approve or dismiss them. A successfully executed proposal leaves Notifications immediately but remains in the saved proposal record for audit; an uncertain outcome stays visible for inspection. The comment plan includes the observed PR head SHA and issue update time; a change stops before posting. Each proposal has a durable request ID, so a process interruption after publication can be reconciled through the saved `writes/` record without posting the comment again. A failed or uncertain close stops the remaining batch. Dismissal excludes a proposal from the active set while retaining its record; marking it viewed changes presentation only.
