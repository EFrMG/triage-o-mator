# Publishing comments and changing item state

Open an issue or PR in the TUI and press `c`. Enter the comment; `Ctrl-P` toggles between editing and a rendered Markdown preview (scroll with the arrow keys). The target URL stays visible in both views. Press `Ctrl-S` once from either view to approve the current text and target and publish. The composer closes after publication; the status line reports the result and the open live item reloads its comments. `Enter` inserts a newline. `Esc` returns from preview to editing or discards the composer from the editor. This posts a conversation comment, including on PRs; it does not post an inline review or change the item's state.

The header shows “Compose comment” in blue while editing or “Preview Markdown” in green while previewing, with the target URL on the right.

Preview is optional and renders locally with the same Markdown renderer as item discussions. It never contacts GitHub. The editor retains its text and cursor when you toggle preview. `Ctrl-Enter` is not a distinct shortcut in the current terminal input stack; use `Ctrl-S` to publish.

Press `C` on an item to compose in `$EDITOR` instead. The TUI pauses while the editor works on a private temporary `.md` file. Save and exit to return to the rendered preview; nothing is sent until you press `Ctrl-S`. In preview, `C` reopens the current draft in the external editor. In the inline editor, capital `C` remains ordinary text.

To close an open issue or PR with an explanation, press `x` on the item, or `X` to compose directly in `$EDITOR`. The composer shows “Close with comment” and the target URL; `Ctrl-S` approves the exact text, target and closure together. It publishes the conversation comment first, then closes the item. The TUI shows the confirmed closed state and reloads that item's discussion immediately, while refreshing the ledger in the background. The comment must explain the closure; the script checks that it contains text but cannot judge the explanation.

Press `v` on a closed issue or PR to reopen it with an explanatory comment, or `V` to compose in `$EDITOR`. On an item list, tick one or more closed items with `Space` and press `v` or `V`; without ticks it uses the hovered item. One comment is shared across the selected items. For multiple items, the first `Ctrl-S` from the inline editor opens a scrollable review with every target URL and the comment; `Ctrl-S` from that review approves the set. `$EDITOR` returns to the same review. The TUI sends one exact-plan request at a time and stops on the first uncertain outcome, showing how many were confirmed open. A merged PR cannot be reopened. The confirmed open state appears immediately; the TUI refreshes the item discussion and ledger afterward.

`EDITOR` may include arguments, such as `nvim` or `code --wait`; the command must wait until editing is finished. If unset, it defaults to `vi`. Imported CRLF newlines become LF and tabs expand to four spaces. Successful imports remove the temporary file; an editor or import failure keeps the current TUI draft and reports the temporary file's path for recovery.

From an install, save the proposed text in a UTF-8 file and preview it:

```sh
bin/comment --expected-repo OWNER/REPO --kind issue --number 123 --body-file /tmp/comment.md
```

The default is an offline dry-run: it prints the target, exact body, `state_change: "none"`, a request ID and an approval hash without saving a proposal or contacting GitHub. `--dry-run` is also accepted. For GitHub Enterprise, include `--host HOSTNAME` in both invocations; the default is `github.com`, independent of `GH_HOST`.

After a human approves that exact plan, repeat the command with the printed values:

```sh
bin/comment --expected-repo OWNER/REPO --kind issue --number 123 --body-file /tmp/comment.md \
  --publish --request-id REQUEST_ID --approve APPROVAL_HASH
```

Use `--kind pr` for a PR. `--body TEXT` is an alternative to `--body-file`. Changing the host, repository, kind, number, body or request ID invalidates the approval hash. The hash binds the plan; it does not authenticate a human or replace their approval. An agent must obtain the human's explicit approval of the target and text before publishing.

For a closure, add `--close` to both commands. The dry-run plan then shows `operation: "close"` and `state_change: "closed"`. Approval covers the comment and state change together. Publication checks that the live item is still open, posts the comment, and sends a `PATCH` with `state: "closed"` to the issues endpoint for an issue or the pulls endpoint for a PR. A changed or already closed target stops before the comment. A failed close after a successful comment leaves the comment published and records the close outcome as unknown.

For reopening, add `--reopen` to both commands instead. The plan shows `operation: "reopen"` and `state_change: "open"`. Publication checks that the live item is closed and, for a PR, that it is not merged before posting. It then comments and sends a `PATCH` with `state: "open"`. A failed reopen after a successful comment leaves the comment published and the state outcome unknown. For a TUI bulk reopening, each item has its own request ID and saved outcome; successful earlier items are not repeated after a later failure.

Publishing verifies the live target, then records the attempt in `data/<owner>/<repo>/writes/<request-id>.json` before sending the comment. Comment and state-change outcomes are separate; comment-only requests record `not_requested` for the state change. These records are local replay-protection and recovery state, owned by `bin/comment` and Git-ignored even in adopted installs. The request ID and approval hash prevent a repeated request from posting again; the attempted text, target and outcome let you inspect an interrupted request. They are separate from evidence and ledger review status. Deleting a record removes replay protection for its request ID. Publishing never marks a decision reviewed or changes cached evidence.

Repeating a successful request returns its saved result without another write. An interrupted request or failed POST or PATCH has an `unknown` outcome: GitHub might already have accepted it. The same request is never automatically retried. Inspect GitHub and the saved record before preparing and approving a new request; a new request ID can post the same text again. Replay protection applies within this install; it is not a GitHub idempotency guarantee.

The implementation uses GitHub's [conversation comment endpoint](https://docs.github.com/en/rest/issues/comments#create-an-issue-comment), shared by issues and PRs, and the [issue](https://docs.github.com/en/rest/issues/issues#update-an-issue) or [PR](https://docs.github.com/en/rest/pulls/pulls#update-a-pull-request) update endpoint for state changes. Automated tests use fake `gh` in disposable installs.

`bin/install-to` maintains the ignore rule. Rerun the installer to apply it to an existing install. Ignore rules do not untrack records already added to Git or remove them from past commits.
