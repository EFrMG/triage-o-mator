"""Literal offline source discovery, never a duplicate verdict or an exhaustive match listing."""

import json

from _chunks import corpus_listing, listing, window
from _evidence import DEFAULT_MAX_AGE
from _evidence import canonical, digest, natural, validate_payload
from _reader import problems

SEARCH_COMPONENTS = ("summary", "comments", "files", "diff", "reviews", "review_comments", "closing_issues")


def strings(value, path=""):
    """Yield JSON string values and RFC 6901 pointers; keys and numbers are not searched."""
    if isinstance(value, str):
        yield path, value
    elif isinstance(value, list):
        for index, child in enumerate(value):
            yield from strings(child, f"{path}/{index}")
    elif isinstance(value, dict):
        for name, child in value.items():
            escaped = name.replace("~", "~0").replace("/", "~1")
            yield from strings(child, f"{path}/{escaped}")


def first_match(payload, component, query):
    values = [("", payload)] if component == "diff" else strings(json.loads(payload))
    for pointer, value in values:
        position = value.find(query)
        if position < 0:
            continue

        start = max(0, position - 60)
        end = min(len(value), position + len(query) + 60)
        return dict(pointer=pointer[:256], pointer_truncated=len(pointer) > 256,
                    character_offset=position, excerpt=value[start:end], excerpt_start=start,
                    omitted_before=start, omitted_after=len(value) - end)

    return None


def search(cache, query, component, snapshot=None, corpus=None, offset=0, limit=20,
           checkpoint=None, max_age=DEFAULT_MAX_AGE):
    window(offset, limit, 100)
    natural(max_age, "maximum age")
    if not isinstance(query, str) or not query.strip() or len(query) > 256:
        raise ValueError("query must contain non-whitespace text and at most 256 characters")

    if component not in SEARCH_COMPONENTS or bool(snapshot) == bool(corpus):
        raise ValueError("select one supported component and exactly one snapshot or corpus")

    if offset and checkpoint is None:
        raise ValueError("search continuation requires --checkpoint from the first page")

    source = corpus_listing(cache, corpus, offset, limit) if corpus and offset == 0 else None
    if corpus and source is None:
        # Read the current state token before asking the existing metadata reader for a later page.
        current = corpus_listing(cache, corpus, 0, 1)
        source = corpus_listing(cache, corpus, offset, limit, current["checkpoint"])
    if not corpus:
        source = listing(cache, snapshot, offset, limit, max_age)

    token = digest(canonical(dict(repository=source["repository"], snapshot=snapshot, corpus=corpus,
                                 state=source.get("checkpoint"), query=query, component=component, max_age=max_age)))
    if checkpoint is not None and checkpoint != token:
        raise ValueError("search scope, query or corpus checkpoint changed; restart at offset zero")

    snapshot_records = None
    if snapshot:
        snapshot_records = {(row["identity"]["kind"], row["identity"]["number"]): row
                            for row in cache.load_manifest(snapshot)["items"]}

    items = []
    for member in source["items"]:
        identifier = member.get("snapshot_id") if corpus else snapshot
        identity = member["identity"]
        record = None
        if snapshot_records is not None:
            record = snapshot_records[(identity["kind"], identity["number"])]
        elif identifier:
            manifest = cache.load_manifest(identifier)
            record = next(row for row in manifest["items"] if
                          (row["identity"]["kind"], row["identity"]["number"]) == (identity["kind"], identity["number"]))

        descriptor = record["components"].get(component) if record else None
        reference = descriptor.get("object") if descriptor else None
        match = None
        if reference:
            payload = cache.read_object(reference)
            validate_payload(component, descriptor, identity["kind"], payload)
            match = first_match(payload, component, query)

        items.append(dict(identity=identity, snapshot_id=identifier,
                          revision=record["revision"] if record else None,
                          object=reference, fetched_at=descriptor["fetched_at"] if descriptor else None,
                          status=descriptor["status"] if descriptor else "missing",
                          problems=problems(record, [component], max_age),
                          available=bool(reference), match=match))

    if corpus and corpus_listing(cache, corpus, 0, 1)["checkpoint"] != source["checkpoint"]:
        raise ValueError("corpus checkpoint changed during search; restart at offset zero")

    next_offset = source["pagination"]["next_offset"]
    return dict(repository=source["repository"], snapshot_id=snapshot, corpus_id=corpus,
                component=component, query=query, checkpoint=token, pagination=source["pagination"],
                items=items, matched_items=sum(row["match"] is not None for row in items),
                continuation=dict(offset=next_offset, limit=limit, checkpoint=token) if next_offset is not None else None,
                requests=0, mode="offline", matching="case-sensitive literal string values; first match per item, no ranking",
                coverage="pagination counts examined members, not matches; missing/partial evidence is not a negative verdict",
                verification="selected component objects verified; other payloads not audited; full selected JSON/objects loaded",
                freshness_basis="recorded revisions and local age, not current GitHub state")
