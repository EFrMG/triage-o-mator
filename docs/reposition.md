# Optional offline ranked retrieval

The Reposition bridge adds `bin/cache search-index`, `query`, `retrieve` and `search-info`. Turning automatic download ON in the TUI's Local dataset menu prepares the pinned engine for the tool checkout. A setup failure records Reposition as OFF and leaves native acquisition and offline search available. Restarting or refreshing does not repeat a failed install; turning automatic download OFF and ON explicitly retries it. The environment is shared by installs linked to that code checkout.

It searches explicit immutable snapshots or frozen corpora and resolves selected hits to verified source fragments. The default literal `bin/cache search` and existing readers remain usable without Reposition, although it is recommended to use it.

## Supported versions and installation

This bridge supports Reposition **0.2.0.dev1**, with evidence-cache format **1**. The checked-in release bundle under `vendor/reposition/0.2.0.dev1/` contains the wheel, the source archive from commit `10bd0641b50d514984dad6dee480f140ab86ee44`, pinned pip and setuptools wheels, an exact-wheel requirements file and a checksummed manifest. Reposition's adapted MIT evidence validator originates at triage-o-mator commit `dddc487660ffda17ba7e3626c9d9be72e29a6dce`; the current-master compatibility check uses `80cdcdd77d875c4fdcd96f3367f2f8a7d253e63a`. This validates the selected build and cache format, not every future Python or Reposition release.

One optional virtual environment lives in the **triage-o-mator code checkout**, not in each repository being triaged. The four ranked-retrieval commands use it automatically through the install's linked `bin/`; shell activation is not needed. The TUI creates it when automatic download is ON. For CLI-only use, `bin/reposition-env enable` installs the approved wheel; `status` and `disable` inspect or turn off the shared environment without deleting sidecars. Setup uses the bundled pip wheel and needs no ensurepip or network connection. Use Python 3.10+ with SQLite FTS5:

```sh
cd /path/to/triage-o-mator
python3 tools/reposition_bundle.py verify
python3 tools/reposition_bundle.py rebuild # optional independent offline build
cd /path/to/your/repository/triage-o-mator
bin/reposition-env enable
bin/reposition-env status
bin/cache query --help
```

The bundle verifier checks every retained file against the manifest digest approved in `bin/_reposition.py`, the wheel's `RECORD`, the installation requirement and license files. `rebuild` uses the retained source and setuptools with the recorded build timestamp and requires the new wheel to match byte for byte. The manifest digest is anchored in this checkout's reviewed code; it is an integrity check, not independent signing or a third-party security audit. The source archive is the retained build input, while the exact wheel is the only Reposition artifact installed. No upstream Git or package index access occurs during setup, indexing, query, retrieval or metadata inspection. There are no runtime models, credentials, servers or required third-party Python dependencies. `status` checks the approved wheel, installed files, version and FTS5; an unverified same-version installation is unavailable and `enable` repairs it from the bundle. An isolated `pipx` installation is not visible to this bridge. The checkout's `.reposition-venv/` is ignored by Git and shared by its installs; ordinary literal search does not need it.

To turn off the option, run `bin/reposition-env disable` from an install; existing literal search remains available. For a future supported engine, audit a new source snapshot and build inputs, regenerate and review the bundle and manifest, update the approved digest in the bridge, then run the compatibility checks. Do not substitute a same-version wheel without a new approved manifest digest. The bridge rejects unapproved code rather than silently adopting it.

Run the synthetic integration check whenever the cache contract, adapter or supported engine version changes. The normal test job verifies the legacy path without the optional package. A separate integration job rebuilds the checked-in wheel offline, installs that exact artifact and checks snapshot/corpus publication, bounded query/retrieval, repository guards and unchanged source artifacts. `tools/reposition_compat.py` runs the retained source trial in a disposable checkout, adapting only its pre-index error expectation because the approved wheel is already installed. Python and SQLite remain host prerequisites; retaining the bundle does not guarantee compatibility with future interpreters or operating systems.

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
