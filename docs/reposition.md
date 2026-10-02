# Optional offline ranked retrieval

The Reposition bridge adds `bin/cache search-index`, `query`, `retrieve` and `search-info`. Turning automatic download ON in the TUI's Local dataset menu prepares the pinned engine for the tool checkout. A setup failure records Reposition as OFF and leaves native acquisition and offline search available. Restarting or refreshing does not repeat a failed install; turning automatic download OFF and ON explicitly retries it. The environment is shared by installs linked to that code checkout.

It searches explicit immutable snapshots or frozen corpora and resolves selected hits to verified source fragments. The default literal `bin/cache search` and existing readers remain usable without Reposition, although it is recommended to use it.

## Supported versions and installation

This bridge supports Reposition **0.2.0.dev1**, with evidence-cache format **1**. The tested engine source is pinned below. Reposition's adapted MIT evidence validator originates at triage-o-mator commit `dddc487660ffda17ba7e3626c9d9be72e29a6dce`; the current-master compatibility check uses `80cdcdd77d875c4fdcd96f3367f2f8a7d253e63a`. This is commit-level validation, not a claim that every release or future cache format is supported.

Create one optional virtual environment in the **triage-o-mator code checkout**, not in each repository being triaged. The four ranked-retrieval commands use it automatically through the install's linked `bin/`; shell activation is not needed. The TUI creates it when automatic download is ON. For CLI-only use, `bin/reposition-env enable` performs the same pinned installation; `status` and `disable` inspect or turn off the shared environment without deleting sidecars. Use Python 3.10+ with SQLite FTS5:

```sh
cd /path/to/triage-o-mator
python3 -m venv .reposition-venv
.reposition-venv/bin/python -m pip install --no-deps \
  'git+https://github.com/Univeracity/reposition.git@10bd0641b50d514984dad6dee480f140ab86ee44'
.reposition-venv/bin/python -c 'import reposition, sqlite3; assert reposition.__version__ == "0.2.0.dev1"; sqlite3.connect(":memory:").execute("CREATE VIRTUAL TABLE fts_check USING fts5(text)"); print("Reposition and FTS5 ready")'
cd /path/to/your/repository/triage-o-mator
bin/reposition-env enable
bin/cache query --help
```

Use this Git source, not the unrelated PyPI package with the same name. Installing requires network access of course; while indexing, query, retrieval and metadata inspection remain offline. There are no runtime models, credentials, servers or third-party Python dependencies. The final command verifies that `bin/cache` reaches the pinned engine; an absent or unsupported version fails before reading a cache. An isolated `pipx` installation is not visible to this bridge. The checkout's `.reposition-venv/` is ignored by Git and shared by its installs; ordinary literal search
does not need it.

To remove the option, run `.reposition-venv/bin/python -m pip uninstall reposition` in the code checkout; existing literal search remains available. For a future supported engine, update the tested commit and version in the bridge and this guide after compatibility checks, then reinstall into `.reposition-venv` with `--force-reinstall`. The bridge rejects untested versions rather than silently adopting them.

Run the synthetic integration check whenever the cache contract, adapter or supported engine version changes. The normal test job verifies the legacy path without the optional package. A separate integration job installs the pinned engine and checks snapshot/corpus publication, bounded query/retrieval, repository guards and unchanged source artifacts. Upgrade the pin and supported version together after compatibility passes.

## Workflow and boundaries

From the selected install:

```sh
bin/cache --expected-repo OWNER/REPO search-index --corpus CORPUS_ID
bin/cache --expected-repo OWNER/REPO query --corpus CORPUS_ID \
  --query 'terminal fails after suspend'
bin/cache --expected-repo OWNER/REPO retrieve --corpus CORPUS_ID \
  --unit UNIT_ID --checkpoint INDEX_CHECKPOINT
bin/cache search-info --corpus CORPUS_ID
```

The query response supplies unit IDs and the index checkpoint. Its excerpts already verify source checksums and exact locators; retrieval opens larger windows. Preserve repository, selection, checkpoint, coverage, omissions and source citations when passing results to a reviewer or later TUI integration. See [the detailed contract](evidence-reference.md#optional-ranked-retrieval-with-reposition) for filters, offsets, budgets and continuation parameters.

## Verify it is being used

From an install, `bin/reposition-env status` reporting `enabled: true` confirms that the pinned engine and SQLite FTS5 load in the tool checkout. `bin/cache usage` reports allocated `reposition` bytes, but those bytes may belong to another or older view. Use `bin/cache handoff` to find the selected corpus ID, then inspect its view with `bin/cache search-info --corpus CORPUS_ID`. A successful `bin/cache query --corpus CORPUS_ID --query 'distinctive phrase' --limit 5` is the read that actually uses the bridge; returned excerpts are verified against the selected immutable evidence. All of these checks are offline. If the selected view does not exist, build it explicitly with `bin/cache search-index --corpus CORPUS_ID` before querying. Automatic download prepares the engine; it does not rebuild a search view at every acquisition checkpoint.

The TUI shows no routine Reposition active indicator. A setup failure appears in Local dataset with its short reason and in `!` error details, while native cache acquisition and offline search continue. A moved Python installation can leave the checkout's virtual environment unable to start; the next explicit automatic-download OFF/ON recreates that dedicated environment and retries the pinned installation. If an install's `bin/` symlink points to an absent checkout, relink it with `bin/install-to TARGET_REPOSITORY` from the current code checkout before using its commands.

`search-info` binds the index to the install's cache repository, including known stable IDs, even with an explicit `--db`. A supplied snapshot/corpus must match the indexed selection. Without a scope, `--db` may inspect another view belonging to the same repository. Metadata inspection does not audit payloads or source checkpoints: its verification flags are false, and stale indexes remain inspectable before rebuilding. Foreign host/name/IDs and conflicting scope fail.

Search results are discovery evidence. Similar symptoms, shared files, ranked scores and literal issue links do not establish duplicate fixes, select a survivor, approve an action or create a closure suggestion. Acquisition, TUI presentation, ledger decisions, proposals and approved writes remain owned by triage-o-mator. Source text remains untrusted data.

## Storage and cost trial

Managed indexes live beside `cache/`, under ignored `reposition/`, and count with evidence toward one practical 5 GB local-cache budget. New installs ignore that directory in their managed block; an index build also creates a local `.gitignore` for older installs. The bridge holds the cache publication lock during a build, caps the new SQLite view by the remaining space and retains the previous view until atomic replacement. Builds with `--db` must stay in the managed `reposition/` directory. Default limits are 512 MiB and one million units **per view**. Delete unused sidecars locally without pruning source history. A failed bounded rebuild keeps the previous index. This is an allocation guard for managed cache files, not a filesystem quota; fetched Git objects in the repository's object store and the optional checkout environment remain outside it. Objects are verified in full, with explicit 64 MiB per-object and 128 MiB per-response verification ceilings by default.

The [synthetic cost and review report](https://github.com/Univeracity/reposition/blob/main/docs/triage-review-trial.md) records source/index size, build time, peak engine-process memory, verified query costs and complete CLI timings at three sizes. It includes same-symptom cases with different causes and changes to the same path. The report separates those authored examples from real backlog quality; synthetic repetition is not a 5 GB qualification or a measurement of duplicate-decision accuracy.

## Resume and continue review

Keep the exact selection, checkpoint, query/filter/budget settings and returned continuation to page more ranked results or read further in a source boundary. Changed index or source progress requires a new query and explicit rebuild; continuation never silently switches to newer evidence. These are bounded read continuations, not a store of a reviewer's investigation history.

For the next trial, use an explicitly selected real cache, record its coverage, measure storage/latency/memory under the same budgets, and independently review duplicate, competing-fix and different-cause examples. Retain disagreements and missing evidence. Let those observations guide TUI presentation and any later suggestion workflow; approved actions remain a separate contract.
