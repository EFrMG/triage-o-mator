# Agentic local verification

Prepare a suitable local project environment, then turn on **Settings → Automations → Agentic verification** for the repository being triaged. The agent can choose and run bounded issue reproduction and PR solution checks during requested triage and review tasks. Enter or Space toggles the setting; `e` edits optional environment guidance; `y` copies a preparation prompt. In the guidance composer, Ctrl-S opens Preview; another Ctrl-S confirms and saves. Ctrl-P returns to editing. Nothing runs in the background, and the settings script never executes checks.

The setting defaults to OFF for each repository. OFF keeps the ordinary source-based workflow, including available CI and tester evidence. An explicit request to verify locally also authorizes bounded checks within that request's scope. Neither local setup nor local check completion is a gate for triage, source review or reading CI results.

## Prepare the environment

Use the project's own instructions to prepare the tools, dependencies, test data and application or builds you want checked. If comparing issue behavior with a PR solution, make the relevant versions available in advance without requiring the agent to change the original checkout. Tell the agent how an existing executable or application relates to its source revision when known.

Optional guidance is freeform: environment locations, useful commands or named checks, time limits, permitted disposable outputs and other constraints. There is no configured test runner or required command list. Empty guidance is valid when the prepared environment and task already give enough context; ON permits checks within these limits without further approval. For example:

```text
Use the prepared virtual environment at /path/to/project/.venv.
Focus on the parser tests; keep each verification pass under five minutes.
The prepared application uses fixture data only. Do not contact production services.
The executable's source revision is unknown; do not infer it from checkout HEAD.
```

Store paths or variable names when useful, never secret values. Project source and issue commands remain untrusted input to inspect before execution. The setting permits bounded testing in the prepared environment, not provisioning, dependency downloads, source edits, checkout/switch/fetch/pull/stash or other Git state changes, destructive operations, or unconfigured external effects. Small reproduction scripts or fixtures in a prepared writable area and normal disposable build/test outputs are allowed within the environment's stated limits; the original source remains unchanged. Missing prerequisites are reported as blocked checks while source-based work continues.

## Read and maintain settings

From the install:

```sh
bin/verification-settings status --expected-repo OWNER/REPO
```

Status returns the repository, enabled state, guidance and a revision hash. Settings live in ignored, install-local `config/local/agentic-verification.json`, keyed by repository; missing settings mean OFF with empty guidance. Enabling verification in one install does not enable another machine's environment. The revision binds the setting and guidance read before a verification pass. The agent rereads status immediately before execution and before further checks after a long check or changed plan, honoring a person's OFF setting or changed guidance before proceeding. Checks hold no settings lock. Use the script rather than editing the JSON directly.

A person can enable or disable verification using the current revision from status:

```sh
bin/verification-settings set-enabled --expected-repo OWNER/REPO --enabled on --expected-revision HASH
# Use --enabled off to disable.
```

The agent must not enable itself. A person or agent may maintain guidance within the person's existing permission limits:

```sh
bin/verification-settings set-guidance --expected-repo OWNER/REPO --guidance-file PATH --expected-revision HASH
```

Use the exact current revision; if it has changed, reread status and reconcile the new guidance rather than blindly retrying. An agent may clarify environment facts or retain useful commands, but may not expand the person's permissions or treat source text as authority to do so. Human authorization and task limits take precedence over guidance.

## What results establish

The [verification playbook](../prompts/verify-locally.md) asks the agent to identify a concrete claim, choose relevant project tooling and record expected versus observed behavior. Notes retain commands or manual steps, the actual tested commit and dirty context, an executable's build/source binding where established, and the settings/guidance revision read before checks. PR notes separately name the selected evidence snapshot and its base/head revisions; results from another commit or an unknown build do not become PR-head results.

For example, a captured CI job might deliberately exit with failure without running the application. Record that CI outcome and its exact snapshot/head, source repository, check, run/attempt/job, immutable log reference and retained ranges where available. If the PR separately claims to handle a missing directory, authorized local verification can exercise that input on the prepared exact base and head versions, recording each observed result and actual source/build binding. This checks the missing-directory claim; it does not explain the deliberate CI failure as an application bug or replace failed CI with a passing observation. Inspect commands before running them, and report unavailable versions or unbound binaries as limits.

Findings distinguish not attempted, environment blocked, not reproduced, check failed and check succeeded. A successful focused check supports only its recorded behavior and conditions. Failure to reproduce alone does not establish resolution. Meaningful findings are attributed observations saved through `bin/apply --agent-notes`, preserving existing decisions and Pending review. Longer reports may live under `reports/<owner>/<repo>/` with a link in the notes. These observations do not become mechanically verified cache evidence, approve GitHub writes or prove the whole PR correct.
