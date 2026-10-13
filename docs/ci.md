# CI observations and readiness

The PR **Checks** tab and `bin/pr-checks` separate observed outcomes from coverage and freshness. They open saved evidence offline; explicit refresh acquires focused metadata within a request budget. Empty observations do not mean checks passed. See [the evidence reference](evidence-reference.md#pr-check-metadata) for history, reruns and snapshot scope.

## Require CI for ready assessments

**Settings → Automations → CI for readiness** defaults to OFF for each repository. Enter or Space toggles it. ON adds a requirement to `bin/pr-assessment record --purpose review --outcome ready`: complete, fresh, nonempty passing CI observations for the exact reviewed PR head. Failed, pending, unknown, skipped, neutral, partial, missing, stale and complete-empty evidence cannot satisfy it. Repositories without CI can leave it OFF. Other review outcomes and closure assessments retain their existing requirements.

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

Keep the returned `log_object` reference for exact offline reading:

```sh
bin/pr-check-log read --expected-repo OWNER/REPO --number N --snapshot CHECKS_HASH --log-object OBJECT_SHA --log-bytes OBJECT_BYTES --max-lines 80
```

The reader verifies the immutable object and its selected parent evidence, and bounds the displayed context to the requested line count (at most 200) and 192 KiB of sanitized text. This byte allowance accommodates replacement characters from at most 64 KiB of retained raw bytes. Follow its continuation with the same object reference, PR and parent snapshot. Use `--start-line` to select a retained source range. A range outside the retained excerpts requires another explicit `fetch --start-line LINE`, optionally with a larger byte cap when necessary; it never triggers an offline download. New captures return new immutable references. Preserve the earlier reference rather than silently replacing its source.

The TUI Checks tab shows failures first, with saved failure excerpts inline. A compact summary retains counts, coverage, freshness, observation time and whether the checks match the selected PR head. Complete-empty observations say that no checks or statuses were recorded; missing evidence remains a separate state. Check, workflow and job names identify failures without exposing internal IDs.

After publishing a log object, `fetch` atomically updates a bounded association for that exact parent checks snapshot, source repository and check. The association only selects an object: readers verify its checksum, parent evidence and source bindings before showing text. A newer partial, unavailable or corrupt capture never falls back to an older success. Earlier immutable references remain readable directly.

Opening Checks reads saved excerpts offline through `bin/pr-checks --include-failure-logs`. Each page bounds the number of excerpts, displayed lines and bytes, and shows source line ranges, omitted context and capture problems. Metadata refresh acquires only check metadata; it never captures logs. A new checks snapshot needs its own explicit log capture. Use the exact-object reader above for more retained context. Follow [Inspect CI](../prompts/inspect-ci.md) to acquire relevant context and retain findings in item notes.
