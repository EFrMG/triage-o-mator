"""Offline watch history v1: stable source/content revisions with frozen raw evidence references."""

from _chunks import page, read, window
from _evidence import canonical, digest, natural
from _watch import load, observation
from _read_commands import command

POLICY = "watch-source-revisions-v1"


def checkpoint(watch, entry=None):
    return digest(canonical(dict(policy=POLICY, watch=watch["checksum"], entry=entry)))


def source_revision(row):
    source = row["source"]
    if row["component"] == "comments" or source.get("event") == "commented":
        # Cross-endpoint comments share only this explicit projection. Raw endpoint differences remain in pinned references.
        if type(source.get("id")) is int and source["id"] > 0:
            key = f"comment:{source['id']}"
            value = {name: source.get(name) for name in ("body", "created_at", "updated_at", "node_id")}
            user = source.get("user")
            value["user"] = {name: user.get(name) for name in ("id", "node_id", "login")} if isinstance(user, dict) else user

            return key, digest(canonical(value))

    return "timeline:" + row["source_id"], digest(canonical(source))


def collect(cache, watch):
    entries = {}
    coverage = dict(observation_count=len(watch["observations"]), last_successful_check=None)
    for position, snapshot in enumerate(watch["observations"]):
        observed = observation(cache, watch, snapshot)
        coverage.update(latest_snapshot=snapshot, latest_complete=observed["complete"], latest_gap_count=len(observed["gaps"]))
        if position == 0:
            coverage["baseline_complete"] = observed["complete"]

        if observed["complete"]:
            coverage["last_successful_check"] = observed["observed_at"]

        for row in observed["activity"]:
            component = row["component"]
            source_id, revision = source_revision(row)
            identifier = digest(canonical(dict(policy=POLICY, repository=watch["repository"], item=watch["item"],
                                               source=source_id, revision=revision)))
            ref = dict(snapshot_id=snapshot, component=component, offset=row["source_offset"],
                       source_digest=digest(canonical(row["source"])), observed_at=observed["observed_at"])
            if identifier not in entries:
                entries[identifier] = dict(id=identifier, source_id=source_id, revision_digest=revision,
                                           relation=row["relation"], first_observed=observed["observed_at"],
                                           last_observed=observed["observed_at"],
                                           source_times={key: row["source"].get(key) for key in ("created_at", "updated_at", "submitted_at")},
                                           references=[])

            entry = entries[identifier]
            entry["last_observed"] = observed["observed_at"]
            entry["references"].append(ref)

    return list(entries.values()), coverage


def bound(cache, number, token=None, entry=None):
    watch = load(cache, number)
    expected = checkpoint(watch, entry)
    if token is not None and token != expected:
        raise ValueError("watch history checkpoint or entry changed/mismatched; expected the checkpoint from watch-history for this repository, PR and selected --entry, not a snapshot ID, polling or local-context checkpoint; restart watch-history with the same --number and --entry at offset zero")

    entries, coverage = collect(cache, watch)

    return watch, expected, entries, coverage


def history(cache, number, offset=0, limit=20, token=None, entry=None):
    window(offset, limit, 100)
    if offset and token is None:
        raise ValueError("history continuation requires --checkpoint")

    watch, token, entries, coverage = bound(cache, number, token, entry)
    if entry is not None:
        selected = next((row for row in entries if row["id"] == entry), None)
        if selected is None:
            raise ValueError("source revision is not in this watch; --entry expects a source-revision id from watch-history for this PR, not source_id, snapshot_id or checkpoint; rerun watch-history without --entry at offset zero")

        rows = selected["references"]
    else:
        rows = [dict((key, value) for key, value in row.items() if key != "references") |
                dict(reference_count=len(row["references"]), first_reference=row["references"][0], last_reference=row["references"][-1])
                for row in entries]

    selected = rows[offset:offset + limit]
    pagination = page(len(rows), offset, len(selected))
    selected = [dict(row, reference_type="watch-raw-reference" if entry is not None else "watch-source-revision",
                     read_command=command("cache", watch["repository"], "watch-source", number=number, entry=entry,
                                          reference=offset + index, checkpoint=token) if entry is not None else
                     command("cache", watch["repository"], "watch-history", number=number, entry=row["id"]))
                for index, row in enumerate(selected)]
    next_command = command("cache", watch["repository"], "watch-history", number=number, entry=entry,
                           offset=pagination["next_offset"], limit=limit, checkpoint=token) if pagination["next_offset"] is not None else None

    return dict(schema_version=1, policy=POLICY, repository=watch["repository"], item=watch["item"],
                checkpoint_type="watch-entry-references" if entry is not None else "watch-history", next_command=next_command,
                checkpoint=token, polling_checkpoint=watch["checksum"], entry=entry,
                results=selected, pagination=pagination, coverage=coverage, requests=0,
                continuation=dict(offset=pagination["next_offset"], limit=limit, checkpoint=token, entry=entry) if pagination["next_offset"] is not None else None,
                meaning="Distinct observed source revisions, not notifications, acknowledgment, appeals or authority. Absence never means deletion or withdrawal.",
                scope="All retained snapshots are verified and scanned; output record count is bounded, total input work is not. Raw endpoint differences remain in references.")


def source(cache, number, entry, reference=0, token=None, byte_offset=0, max_bytes=4096):
    natural(reference, "reference index")
    window(byte_offset, max_bytes, 65536)
    if token is None:
        raise ValueError("source reads require the entry reference page's --checkpoint; run watch-history with the same --number and --entry at offset zero, then use its read_command")

    watch, token, entries, coverage = bound(cache, number, token, entry)
    selected = next((row for row in entries if row["id"] == entry), None)
    if selected is None or reference >= len(selected["references"]):
        raise ValueError("source reference is not in this watch revision; --entry expects the source-revision id and --reference its raw-reference index from watch-history for this PR")

    ref = selected["references"][reference]
    chunk = read(cache, ref["snapshot_id"], "pr", number, ref["component"], ref["offset"], 1, byte_offset, max_bytes)
    # A source continuation is confined to this one saved row, even if the component has more rows.
    continuation = dict(entry=entry, reference=reference, checkpoint=token, byte_offset=byte_offset + chunk["bytes"]["returned"], max_bytes=max_bytes) if chunk["bytes"]["omitted_after"] else None
    chunk["continuation"] = None
    chunk["next_command"] = None
    next_command = command("cache", watch["repository"], "watch-source", number=number, **continuation) if continuation else None

    return dict(schema_version=1, policy=POLICY, entry=entry, reference=ref, checkpoint=token,
                checkpoint_type="watch-entry-references", next_command=next_command,
                source=chunk, continuation=continuation, coverage=coverage, requests=0)
