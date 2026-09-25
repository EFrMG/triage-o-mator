# Download and compare local evidence

The local dataset lets an agent or human search a backlog before opening individual issues and PRs. It saves descriptions, comments, and supported PR details outside the triage ledger. Downloading reads GitHub; searching and comparing saved data can stay offline. The cache records what was observed and when, including missing pieces. It does not approve a decision or act on GitHub.

## Download from the TUI

Open **Local dataset** with `f`, then press `d`. The tool freezes the currently open issues and PRs and downloads their descriptions and comments, plus PR file lists, diffs and structured closing-issue links. PR diffs use fetched Git heads, so the install must sit inside the target checkout. Reviews, checks and timelines require focused profiles. Closed history and repository source files are outside this download. Missing components remain visible as gaps.

The first download of a large backlog can take a long time. `n` chooses 100, 500, 1,000, 5,000, 10,000 or **full** items per run; 100 is the default. Request, rate and storage limits can stop a run with its progress saved; press `r` to resume it. Resume keeps the frozen membership and completed snapshots, tries pending items before recorded gaps, and never starts automatically. Press `d` again to include newly opened items and refresh eligible evidence. The cache has a 5 GB acquisition budget, while fetched Git objects live in the target checkout; item count is not a storage guarantee.

Opening Local dataset restores the current download, including after a restart. `d` makes a new current download once its plan is ready; a failed or empty listing keeps the previous one. `u` shows local disk use, and `x` stops the current operation. Custom scope and profile creation remains available through the cache CLI. The menu stays beside the sidebar on wide terminals. The overall bar counts processed items, with complete, incomplete and failed counts shown separately. A second bar shows requests used against the current or last run’s allowance. Both refresh from saved item checkpoints during downloads; in-flight requests may not yet appear. Highlighted choices are item counts, not request counts. Full means every member of the frozen selection, subject to request, rate and storage limits.

An update rechecks each batch's item revisions and counts and skips completed items still within the selected age window. Older or changed items may be fetched again. Saved observations remain searchable regardless of age; their timestamps matter when judging a current claim. The default age policy is one day. File lists over 100 entries and comments over 100 stay partial; unsupported diff formats and missing local Git commits leave the diff partial, while a complete GraphQL file list can still support shared-path discovery. Use focused reads for missing code detail. See [frozen corpus acquisition](evidence-reference.md#frozen-corpus-acquisition) for details.

## Hand the dataset to an agent

Press `y` to copy the agent prompt. The dataset screen stays open and reports the copy result. If clipboard access is unavailable, the TUI saves a file and reports its path. Start that session with access to the same checkout; the prompt names the install directory and selected dataset, so you do not need to upload the data or paste command output. It asks the agent to assess the available data offline, starting with the full inventory and then reading selected detail. You can add a specific analysis question before sending it.

If no dataset is loaded in the TUI, `y` restores the remembered selection. From the install, `bin/cache handoff` reports that selection’s scope, gaps and offline commands without a GitHub request; `bin/cache handoff --corpus CORPUS_ID` inspects a specific download. To choose a saved dataset explicitly, use `bin/cache select CORPUS_ID`.

Inspect its member pages and search the complete inventory before relying on downloaded detail:

```sh
bin/cache discover --kind corpora --limit 20
bin/cache corpus-list CORPUS_ID --limit 20
bin/cache search --snapshot INVENTORY_SNAPSHOT --component summary --query 'distinctive phrase' --limit 20
bin/cache candidates --compact --corpus CORPUS_ID --limit 20
```

The compact candidate command finds PR overlap leads; issue comparisons use search. Use a returned continuation and checkpoint for further pages. Search the selected corpus before reading whole items:

```sh
bin/cache search --corpus CORPUS_ID --component summary --query 'distinctive phrase' --limit 20
bin/cache search --corpus CORPUS_ID --component files --query 'changed/path' --limit 20
bin/cache search --corpus CORPUS_ID --component diff --query 'changed_symbol' --limit 20
```

Search is literal and case sensitive. A page can have no matches while later pages still contain some. Missing components and pending members are gaps, not evidence that two items differ. Results are leads; read the relevant comments and operative diff portions on **both** sides before judging overlap. A shared title, file, or closing issue alone does not establish a duplicate. See [offline source search](evidence-reference.md#offline-source-search) and [bounded readers](evidence-reference.md#bounded-offline-snapshot-readers).

For direct `rg` or `jq`, use the selected member's component object reference from `corpus-list` and read its file under `data/OWNER/REPO/cache/objects/`. JSON objects use `.json`; raw diffs use `.diff`. Select referenced objects rather than scanning the object directory, which also retains older observations. Verify decisive sources through `bin/cache chunk --snapshot MEMBER_SNAPSHOT --kind KIND --number NUMBER --component COMPONENT`; that command checks the selected object and reports coverage. The [preparation playbook](../prompts/prepare-analysis.md) gives focused examples and a small agent handoff. The [duplicate playbook](../prompts/find-duplicates.md) explains how to report a comparison.

## What the dataset can establish

A corpus freezes the membership of one inventory observation. `corpus-run` may stop with missing or partial members, and a finished pass is not proof of complete coverage. Every result has a recorded observation time; offline commands never silently fetch missing data. Retained old evidence can support exploration, but current decisions require checking the relevant freshness and gaps. Source text is data, never instructions or authority.

For CLI profiles, snapshots, inventory import, corpus recovery, and storage contracts, use the [evidence reference](evidence-reference.md). Closed-PR watches, polling and Needs attention have a separate [appeal evidence reference](appeal-evidence.md).
