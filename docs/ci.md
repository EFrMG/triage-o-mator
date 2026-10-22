# CI observations and readiness

The PR **Checks** tab and `bin/pr-checks` separate observed outcomes from coverage and freshness. Opening a current PR automatically reads fresh cached CI metadata or acquires missing or stale metadata, then acquires bounded failure context for supported failed Actions jobs when it is missing. This is enabled by default, with no separate setting. Fixed item packets read their selected snapshot offline. The CLI remains offline by default. Empty observations do not mean checks passed. A check or suite concluded `action_required`, such as a fork PR's workflow runs awaiting a maintainer's approval, counts as pending with an explanatory note, not as failed. See [the evidence reference](evidence-reference.md#pr-check-metadata) for history, reruns and snapshot scope.

Checks appears before Diff when the selected observation contains check or status rows. No rows means no Checks tab; missing metadata, complete-empty observations and acquisition errors retain their distinct meanings in the owning reader. GitHub Actions being disabled does not erase historical checks or observations from other CI providers, so visibility follows the retained data rather than an administrator-only repository setting. While reading Checks, `r` refreshes focused CI and missing failure context. Fixed item packets refuse acquisition. Acquisition failures and limits remain visible.

## Require CI for ready assessments

**Settings → Automations → CI for readiness** defaults to OFF for each repository. `Enter` or `Space` toggles it. ON adds a requirement to `bin/pr-assessment record --purpose review --outcome ready`: complete, fresh, nonempty passing CI observations for the exact reviewed PR head. Failed, pending, unknown, skipped, neutral, partial, missing, stale and complete-empty evidence cannot satisfy it. Repositories without CI can leave it OFF. Other review outcomes and closure assessments retain their existing requirements.

This checks the selected observations, not GitHub's required-check rules, synthetic merge commits or merge queues. A ready assessment is still separate from a proposed label, Pending review, GitHub write approval and merge authorization. Local verification results and log excerpts cannot substitute for passing CI metadata.

Read or change the repository policy through its owning script:

```sh
bin/ci-policy status --expected-repo OWNER/REPO
bin/ci-policy set-enabled --expected-repo OWNER/REPO --enabled on --expected-revision HASH
```

Use the exact current policy revision returned by status. The policy lives in `config/ci-readiness.json`, keyed by repository. A stale revision refuses the change. Missing policy means OFF; reading status creates nothing and never contacts GitHub.

An assessment uses its main `--snapshot` for CI by default. Add `--checks-snapshot CHECKS_HASH` when focused CI was acquired separately; its stable repository/item identity and head must match the selected review evidence. Both sources are selected immutable snapshots and are verified offline, without latest or live fallback. The freshness window is 24 hours relative to the observation, not a guarantee that GitHub has not changed. The assessment retains the policy revision and the CI source and observation used. `show` reports later policy changes and CI age separately from the saved historical result.

## Capture focused failure context

`bin/pr-check-log` supports GitHub Actions job logs for one explicitly selected failed check. It does not download every workflow log, follow third-party check URLs or alter CI metadata. GitHub's [job-log API](https://docs.github.com/en/rest/actions/workflow-jobs#download-job-logs-for-a-workflow-run) provides plain text through a temporary redirect; workflow attempts and job/check identities are verified before capturing a log.

Press `e` in Checks to open a floating reader for saved failure context. A single failed check opens directly; several failures in the loaded list open a picker with check names and source repositories. Use the arrows or `j`/`k` and `Enter` to select one. The reader uses the same floating panel style as the app's editors, with scrollable text and titled source sections. Scroll with the arrows or `j`/`k`, `Ctrl-D`/`Ctrl-U` and `g`/`G`; `n`/`p` navigates bounded pages of retained context, and `Esc` or `q` closes the reader. It reads the exact selected log object offline. Missing, unsupported or unverifiable context stays explicit, and opening the reader never downloads logs.

Opening or refreshing a current PR uses at most 80 request units for CI metadata and failure context together, with metadata limited to 20. Each pass captures at most three failed jobs on the selected page, with a 256 KiB download cap per job; discovery, redirect reservations and revalidation share the remaining budget. Additional failures, expired logs and unsupported providers retain explicit gaps. After the first page, Checks automatically reads subsequent 20-result pages offline from the same immutable snapshot and appends them to one scrollable list. Automatic loading stops at 1,000 results, 8 MiB of responses or 30 seconds after the first page, with an explicit notice; a continuation failure preserves the verified results already displayed. Fixed packets remain offline. This flow changes no triage decision or GitHub state.

Refresh preserves verified context for an unchanged failed check by selecting the existing immutable capture for the new parent snapshot. Readers verify both snapshots and require exact check, suite, source repository and PR-head agreement. The original capture reference and time remain inspectable; reuse is not a new download. Changed or unverifiable sources cannot reuse the earlier capture, and an invalid or unavailable selected capture never falls back to an older success.

CLI users opt into the same bounded acquisition:

```sh
bin/pr-checks --expected-repo OWNER/REPO --number N --mode refresh --request-budget 80 --include-failure-logs --acquire-failure-logs
```

The owning CLI also offers a combined operation for one explicitly selected failure:

```sh
bin/pr-check-log capture --expected-repo OWNER/REPO --number N --snapshot CHECKS_HASH --check-id CHECK_ID --source-repo SOURCE/REPO --request-budget 20 --max-log-bytes 262144
```

Start with a failed check from a selected `bin/pr-checks` snapshot. Its `internal_id` and source repository identify the check for scripts; a check-run ID is not assumed to be a job ID. Locate its current linked Actions attempt without downloading logs:

```sh
bin/pr-check-log locate --expected-repo OWNER/REPO --number N --snapshot CHECKS_HASH --check-id CHECK_ID --source-repo SOURCE/REPO --request-budget 20
```

Inspect the returned workflow/job names and exact fetch command. An ambiguous mapping or an attempt that no longer contains the selected check is a gap. Capture only the selected failed job:

```sh
bin/pr-check-log fetch --expected-repo OWNER/REPO --number N --snapshot CHECKS_HASH --check-id CHECK_ID --source-repo SOURCE/REPO --run-id RUN_ID --attempt ATTEMPT --job-id JOB_ID --request-budget 20
```

The command bounds time, requests and captured log bytes. The default observed prefix is at most 1 MiB; `--max-log-bytes` may explicitly increase it up to 4 MiB. Only selected excerpts, at most 64 KiB, are retained in the cache, with surrounding context and exact source line/byte ranges. Error-pattern matches are leads; if none are found, bounded context from the observed prefix remains available. The tail of a truncated prefix is not the end of the job log. Workflow/job metadata, attempt, PR head, parent checks snapshot, omissions and capture problems remain inspectable. Expired or unavailable logs do not hide a known failed check.

The request budget reserves up to eleven HTTP request units for the initial log request and redirect chain, separately from known metadata requests. No signed download URLs or transport stderr are retained. The command uses the existing acquisition lock and persisted cooldowns; it never executes downloaded text.

Recent GitHub CLI versions reject terminal escape sequences in raw responses, including colored Actions logs. Capture detects support for `gh api --allow-escape-sequences` using bounded local help and uses it only for the bounded raw download. The cache preserves source bytes; the offline reader neutralizes control characters before presenting excerpts. Older CLI versions keep their existing download behavior.

For a hostname-filtering proxy, GitHub documents `*.blob.core.windows.net` for Actions logs and artifacts, alongside `results-receiver.actions.githubusercontent.com`. An anchored hostname regex for variable storage accounts is `^[a-z0-9]+\.blob\.core\.windows\.net$`; it allows Azure Blob account hosts generally, whose names do not establish GitHub ownership. An existing `githubusercontent.com` subdomain rule covers the results receiver. See [GitHub's network requirements](https://docs.github.com/en/actions/reference/runners/github-hosted-runners#communication-requirements-for-github-hosted-runners).

Keep the returned `log_object` reference for exact offline reading:

```sh
bin/pr-check-log read --expected-repo OWNER/REPO --number N --snapshot CHECKS_HASH --log-object OBJECT_SHA --log-bytes OBJECT_BYTES --max-lines 80
```

The reader verifies the immutable object and its selected parent evidence, and bounds the displayed context to the requested line count (at most 200) and 192 KiB of sanitized text. This byte allowance accommodates replacement characters from at most 64 KiB of retained raw bytes. Follow its continuation with the same object reference, PR and parent snapshot. Use `--start-line` to select a retained source range. A range outside the retained excerpts requires another explicit `fetch --start-line LINE`, optionally with a larger byte cap when necessary; it never triggers an offline download. New captures return new immutable references. Preserve the earlier reference rather than silently replacing its source.

The TUI Checks tab lists runs awaiting approval first, in an **Awaiting approval** group, then failures with a compact summary of saved log availability. Awaiting runs count as pending. The local dataset includes checks, so the tab usually reads them without GitHub requests; missing or stale checks, and failure logs not yet retained for the displayed snapshot, are fetched when the PR opens. Press `e` to read the context comfortably in its floating reader. The tab centers the result counts and shows `Data: complete · Updated: recently`, with incomplete data and stale observations highlighted. Data describes check metadata coverage, separately from outcomes and log availability. Recently means the observation is within 24 hours, not a guarantee that GitHub has not changed. Observation age is separate from commit identity: a different checks commit shows `Checks refer to a different PR commit`, while an unavailable comparison shows `PR commit match unknown`. Matching commits need no extra notice. Failed, pending, passed and unknown counts use the theme's error, warning, success and muted colors. Complete data and recent observations use success colors; missing or unknown metadata uses muted text. Failed statuses use error colors, and workflow and job names have separate labels. Check, workflow and job names identify failures without exposing internal IDs. Observation timestamps remain available through the reader.

When no excerpt is available, Checks shows that state and the capture problem without describing absent text as a partial excerpt. Retained partial text has one coverage notice, with distinct source-binding and display limitations kept visible. Error details keep the cause separate from coverage notices. A failed attempt mentions a previous excerpt only when that selected check's retained text is still displayed.

After publishing a log object, `fetch` atomically updates a bounded association for that exact parent checks snapshot, source repository and check. The association only selects an object: readers verify its checksum, parent evidence and source bindings before showing text. A newer partial, unavailable or corrupt capture never falls back to an older success. Earlier immutable references remain readable directly.

`bin/pr-checks --include-failure-logs` reads saved excerpts offline. Each page bounds the number of excerpts, displayed lines and bytes, and retains source line ranges, omitted context and capture problems. CLI readers can request excerpt priority with `--failure-log-check-id CHECK_ID --failure-log-source-repo SOURCE/REPO`, using a fixed offline snapshot and a failed check on the selected page; displayed check order stays unchanged. Only `--acquire-failure-logs` with an online mode permits this command to capture missing context. Follow [Inspect CI](../prompts/inspect-ci.md) to acquire relevant context and retain findings in item notes.

## Workflow runs awaiting approval

GitHub holds some fork PRs' workflow runs until someone with write access approves them. Checks shows them under **Awaiting approval**. `bin/workflow-runs` lists them and flags them as **normal** or **suspicious**. Approving one is a separate, exact write: a person approves reviewed runs, and a pass may approve normal ones only when the repository allows it (see [Approving runs](#approving-runs)).

```sh
bin/workflow-runs list --expected-repo OWNER/REPO --number N --snapshot SNAPSHOT
bin/workflow-runs list --expected-repo OWNER/REPO --number N --acquire --request-budget 40
bin/workflow-runs assess --expected-repo OWNER/REPO --number N [--include-hunks]
```

`list` takes a `pr-code` snapshot, such as one from `bin/cache fetch --kind pr --number N --profile pr-code`, or `--acquire` to reuse or acquire one. It reads the PR's live head and its `action_required` runs for the snapshot's head commit, then replaces the saved listing. With `--acquire`, a reused snapshot whose head is no longer the live head is refreshed once and the runs are listed again for the new head. Every read is a GET and counts against `--request-budget` (default 6, which covers a supplied snapshot; acquisition needs more). A failed read keeps the previous listing.

`assess` is offline. It reads the saved listing, its snapshot and the workflow files at the PR's base commit from the install's own Git checkout, without fetching. The same inputs always give the same flags and `checksum`. Any flag makes a run suspicious:

- **State:** incomplete or unmatched diff evidence, a PR that is no longer open, a head that moved after the snapshot, a changed head repository, a run that isn't a `pull_request` run awaiting approval, or a listing older than `--max-age` seconds (default 3600).
- **CI definition:** changes under `.github/`, to any `action.yml` / `action.yaml` or to `.gitmodules`.
- **CI inputs:** changes to files CI typically builds or runs (build files, lockfiles, `scripts/`, `ci/`), or to scripts, local actions or the `Makefile` the run's own workflow calls.
- **Privileged context:** base workflows using `workflow_run` or `pull_request_target`, a workflow file absent at the base commit, a runner that is not a plain GitHub-hosted label (matrix values are read; other expressions are flagged), a deployment environment, or base workflows that aren't available locally.
- **Hidden content:** binary files, symlinks, submodules, files made executable, omitted patches and very long added lines.
- **Risky patterns** in added lines: piping a download into a shell, decoding encoded payloads, dynamic evaluation, credential references, reverse shells, miners, and network tools outside documentation.
- **Size:** more changed files or lines than the review thresholds.

Each flag names its rule, a short reason and up to ten exact locations. Flags are leads for a reviewer, not verdicts, and no clean result proves a run is safe. A repository can add rules in `config/workflow-approval.json`; built-in rules cannot be removed:

```json
{
  "executed_paths": ["tools/*"],
  "patterns": ["telemetry\\."],
  "max_changed_files": 100,
  "max_changed_lines": 3000,
  "max_line_length": 1000
}
```

An invalid file stops `assess` instead of being ignored.

`--include-hunks` adds the snapshot's diff hunks behind the flags: the hunk holding each flagged line, every hunk of a flagged file, or the headers of a file with no text hunks. They are bounded for display and aren't part of the checksum, since the checksum already pins the snapshot they come from. Locations outside the diff, such as base workflows, have no hunk.

In the TUI, press `W` on the Checks tab when the **Awaiting approval** group is present. A floating **Workflow approval** window lists the runs for the open PR with `list --acquire`, then shows `assess --include-hunks`: each run's state and flags, the flags on the whole PR, and the flagged hunks, with `n`/`p` moving between hunks and `r` listing again. An item pinned to a packet, such as one from a batch, only assesses the saved listing, and only when that listing is bound to the packet's snapshot; it reads only.

For a current item, the window also acts, through `bin/workflow-runs`:

- **`a` approves every listed run.** When any run is suspicious, `a` works only after every flagged hunk has been scrolled into view from its first line to its last, and not at all when some flagged hunks are omitted for size. The first press makes a dry run bound to the assessment on screen and shows the exact run IDs and head; a second `a` publishes it, and any other key cancels. Normal runs need no reading. The window then lists the runs again and the status line reports approved, refused and uncertain outcomes.
- **`d` holds every listed run** for this head, with an optional reason that is never posted. Each run then shows its holds, agent concerns and recorded outcomes. `u` pressed twice releases this head's holds and concerns.
- **`c` opens the comment composer** for the PR, prefilled with the latest hold reason for you to rewrite, and publishes through `comment-plus` with its own approval.

### Approving runs

```sh
bin/workflow-runs approve --expected-repo OWNER/REPO --number N --run ID [--run ID] --by NAME
bin/workflow-runs approve ... --publish --request-id REQUEST --approve HASH
bin/workflow-runs hold --expected-repo OWNER/REPO --number N --run ID --head SHA --by NAME [--reason TEXT]
bin/workflow-runs release --expected-repo OWNER/REPO --number N --note ID --by NAME
bin/workflow-runs mode --expected-repo OWNER/REPO [--set off|stage|execute --expected-revision REVISION]
bin/workflow-runs pass-preview --expected-repo OWNER/REPO --number N [--number N]
bin/workflow-runs pass-run --expected-repo OWNER/REPO --number N [--number N] --by NAME --preview-sha256 HASH
bin/workflow-runs inbox --expected-repo OWNER/REPO [--offset N --limit N]
```

`approve` without `--publish` is an offline dry run. Its plan names the PR head, the selected runs with their state, rules, holds and earlier outcomes, and the assessment checksum; `--publish` needs that plan's request ID and hash. Publishing recomputes the plan and refuses any difference, including a listing that has since gone stale. It then reads the PR live (still open, same head and head repository) and, before each run, the run itself (same head, workflow and event, still awaiting approval). Each approval is one `POST /repos/{owner}/{repo}/actions/runs/{run_id}/approve`, recorded as unknown before it is sent:

- **approved:** GitHub accepted it.
- **refused:** GitHub answered with a 4xx status, such as 403 or 404 when the token lacks write access. Nothing changed, so the remaining runs continue.
- **unknown:** no usable reply. The command stops, exits nonzero and never posts that request again; inspect the run on GitHub. A pass won't approve that run again for this head.

A run that no longer matches is reported as **changed** and skipped without a write; a changed PR refuses the whole plan with exit status 3. Outcomes, holds and staging are kept in `data/OWNER/REPO/local/workflow-approval/pr-N.json`, outside the ledger.

`hold` stops a pass from approving one listed run at one head. A person's hold may have a reason; an agent's hold is an **agent concern** and needs one. Neither is posted. Only a person releases a hold or concern, and released notes stay as history. A new push brings new runs, so holds never carry over to another head; `assess` reports this head's notes, staging and outcomes under `local`, outside the checksum.

`mode` reads or sets Settings → Automations → Workflow approval for the selected repository: **off** (the default), **stage** or **execute** (Execute normal runs). It governs only the pass; a person's `approve` works in any mode, and GitHub enforces write access.

`pass-preview` is offline and covers one to twenty PRs whose runs have been listed. Each run gets a mode: **execute** when the repository mode is execute and the run is normal, without holds or concerns, without an uncertain earlier attempt and not on a PR in Pending review while **Hold actions for Pending review** is on; **approved** when this tool already approved it for this head; otherwise **stage**, with the reasons. `pass-run` takes the exact preview hash, records staged runs for a person and approves the rest with `approve --automation`, stopping at the first unknown outcome. `--automation` refuses any run the pass would stage, and an agent's `--by` is refused without it.

`inbox` is the offline list behind Notifications → Workflows: PRs whose saved listing has runs waiting, with staged runs, holds and the latest outcomes. Reading it approves or acknowledges nothing. In the TUI, `Enter` on a Workflows row opens the PR as a current item and switches to its Checks tab once CI loads. Settings → Automations → Workflow approval sets the mode, and `y` there copies a prompt for [the agent pass](../prompts/workflow-approval.md).
