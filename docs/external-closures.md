# External closure records

The internal contract in `bin/_external_closures.py` separates an observed closure operation, retained observations and attributed explanations. The explicit-ID importer records one PR from a selected immutable snapshot, and the offline reader retains original explanations and corrections. Notifications places unviewed imported actions in **Needs attention**; `v` moves them to **Past actions**. The menu opens the bounded offline history reader.

## Identity and attribution

A history binds repository and PR identities, including host, full name, number, database ID and node ID. A number or account name alone does not establish identity or provenance.

A retained timeline `closed` event can identify a closure operation. A closed summary alone records an observation with unknown operation identity. Matching closure times do not resolve that uncertainty. Close/reopen/close can involve multiple operations on the same PR, and a historical closure can remain relevant after the PR reopens. Merged state remains separate from unmerged closure.

Claims retain attribution, supplied external identifiers, rationale, survivor and provenance. Unknown provenance stays unknown; personal accounts and comment wording do not establish automation. A supported attribution claim is still not an authentication of the runner, its policy, credentials or authority. Original and competing explanations remain inspectable.

## Record and validation boundaries

Version 1 uses artifact `external-closure-history` and policy `external-closure-v1`; the executable validator is the schema reference. Histories append imports, observations, corrections and competing claims without discarding predecessors. Corrections and competing claims require a reason, predecessor and current history checkpoint. Exact retries preserve the saved entry and ingestion time.

Import verifies the selected snapshot, repository and PR identity, selected source bytes and source positions without fetching. A timeline closure source must be a `closed` event; a closed summary without one retains unknown operation identity. Missing or corrupt selected evidence fails the import. Structural checks and hashes do not prove that a source supports a claim or authenticate its attribution.

## Relationship to watches and handoff

Import must not rewrite an existing watch's initial closure context, enroll a watch implicitly or acknowledge activity. Those fields affect which source revisions count as activity after the closure. An offline reader may derive a watch link from matching identities and report the separate watch checkpoint.

The record location is `data/<owner>/<repo>/external-closures/`, outside the ignored cache. Tracking a record does not preserve the cache objects it references, and a solo install may itself be ignored. Retain the record and its referenced evidence together. Import does not grant human approval or execution authority.

## Explicit import and offline inspection

Run these commands from an install. Acquisition is a separate explicit step; import never fetches missing evidence.

```sh
bin/cache closure-import --number 123 --snapshot SNAPSHOT_ID --by reviewer --closure-event 456 --comment 789 --claim claim.json
bin/cache closure-show --number 123
```

The claim file has the exact version-1 shape:

```json
{
  "external": null,
  "actor": null,
  "run_id": null,
  "rationale": "Supplied explanation, retained as an attributed claim.",
  "survivor": null,
  "provenance": "unknown",
  "supports": []
}
```

When supplied, `external` contains `namespace` and `record_id`. Repeat `--comment` to select multiple discussion IDs. Source indexes in `supports` refer to the summary first, the optional closure event next, then comments in command order. Selecting a comment does not establish that it caused the closure. Without `--closure-event`, only a closed summary can supply a fallback observation, with unknown operation identity explicitly retained.

To correct or contest a saved claim, supply `--kind correction` or `--kind competing`, `--predecessor ENTRY_ID`, `--reason TEXT` and the full `--checkpoint CHECKSUM` returned by the reader. Use `--kind observation` for changed evidence under the same claim/operation. Changes to existing history require its current checkpoint; exact retries preserve saved bytes and ingestion times. Inspect a stale checkpoint failure and reconcile the intended change before retrying.

Imports audit the selected summary and selected discussion/timeline components, including their identities, revisions and source selectors. They do not audit unrelated snapshot objects or authenticate external attribution. The reader preserves unavailable evidence as explicit audit gaps. Watch linkage is derived separately and never changes enrollment, initial closure context or acknowledgments. `closure-show` output and retained-history validation currently load full history. The bounded action readers below paginate output.

## Bounded history and watch navigation

Use **Notifications** in the sidebar, or the offline readers below, to inspect retained histories independently of ledger membership:

```sh
bin/cache action-list --limit 10
bin/cache action-read --number 123 --section entries --checkpoint HISTORY_CHECKSUM
bin/cache action-read --number 123 --section sources --entry ENTRY_ID --checkpoint HISTORY_CHECKSUM
bin/cache action-source --number 123 --entry ENTRY_ID --reference -1 --checkpoint HISTORY_CHECKSUM --max-bytes 4096
bin/cache action-source --number 123 --entry ENTRY_ID --reference 0 --checkpoint HISTORY_CHECKSUM --max-bytes 4096
```

Catalog continuation binds catalog membership and record bytes; detail/source continuation binds the complete history checksum. Pages retain original and competing claims, explicit omissions and pinned source references. Reference `-1` reads the complete attributed entry in UTF-8 fragments; nonnegative indexes read selected evidence sources. Entry fragments validate the record binding and do not claim a payload audit. Output is bounded, while input decoding and retained-history verification can still use whole records in memory. Missing or corrupt evidence stays unavailable; the reader never fetches it.

An existing watch link carries its own checksum and identity-only audit scope. Following it opens the offline Needs attention reader, which verifies the selected watch evidence and rejects changed watch state. The closure history and initial watch closure context remain separate. Reading never enrolls a watch, acknowledges activity or grants approval.

For a requested reassessment, follow [the appeal review playbook](../prompts/review-appeal.md). It keeps imported claims, explicit watch enrollment and source review separate.
