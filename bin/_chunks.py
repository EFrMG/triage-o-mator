"""Bounded offline views of immutable evidence; source strings are data, never instructions."""

import json

from _evidence import DEFAULT_MAX_AGE
from _evidence import COMPONENTS, canonical, digest, natural, validate_payload
from _reader import problems


def window(offset, limit, maximum):
    natural(offset, "offset")
    natural(limit, "limit", 1)
    if limit > maximum:
        raise ValueError(f"limit must not exceed {maximum}")


def page(total, offset, returned):
    if offset > total:
        raise ValueError("offset exceeds available content")

    end = offset + returned
    return dict(total=total, offset=offset, returned=returned, omitted_before=offset,
                omitted_after=total - end, next_offset=end if end < total else None)


def listing(cache, snapshot_id, offset=0, limit=20, max_age=DEFAULT_MAX_AGE):
    window(offset, limit, 100)
    natural(max_age, "maximum age")
    manifest = cache.load_manifest(snapshot_id)
    records = manifest["items"]
    selected = records[offset:offset + limit]
    return dict(snapshot_id=snapshot_id, repository=manifest["repository"],
                pagination=page(len(records), offset, len(selected)),
                verification="manifest and descriptors only; payloads are verified on component read",
                freshness_basis="recorded revision and local age, not current GitHub state",
                items=[dict(identity=row["identity"], revision=row["revision"],
                            components={name: dict(status=value["status"], object=value["object"])
                                        for name, value in row["components"].items()},
                            problems=problems(row, sorted(COMPONENTS), max_age)) for row in selected],
                requests=0)


def corpus_listing(cache, identifier, offset=0, limit=20, checkpoint=None):
    from _corpus import PROFILES, key, load_plan, load_state, observed_state, pinned

    window(offset, limit, 100)
    if offset and checkpoint is None:
        raise ValueError("corpus continuation requires --checkpoint from the first page")

    plan = load_plan(cache, identifier, metadata_only=True)
    state = observed_state(cache, identifier, plan, load_state(cache, identifier, plan, metadata_only=True))
    # An unstarted corpus has no on-disk timestamp; normalize the synthetic read time for stable pagination.
    if state["last_run"] is None and state["status"] == "pending" and all(entry["attempts"] == 0 for entry in state["items"].values()):
        state["updated_at"] = None

    token = digest(canonical(state))
    if checkpoint is not None and checkpoint != token:
        raise ValueError("corpus checkpoint changed or mismatched; restart discovery at offset zero")

    members = plan["members"][offset:offset + limit]
    pagination = page(len(plan["members"]), offset, len(members))
    counts = dict(pending=0, running=0, complete=0, gaps=0, error=0)
    for entry in state["items"].values():
        counts[entry["outcome"]] += 1

    items = []
    for member in members:
        entry = state["items"][key(member)]
        record = pinned(cache, entry["snapshot_id"], member, metadata_only=True) if entry["snapshot_id"] else None
        diagnostics = problems(record, PROFILES[plan["profile"]], plan["max_age"])
        if entry["outcome"] == "complete" and any(problem != "observation outside freshness window" for values in diagnostics.values() for problem in values):
            raise ValueError("complete corpus member references incomplete evidence")

        items.append(dict(identity=member, snapshot_id=entry["snapshot_id"], attempts=entry["attempts"],
                          outcome=entry["outcome"], error_present=entry["error"] is not None,
                          observed_identity=record["identity"] if record else None,
                          revision=record["revision"] if record else None,
                          components={name: dict(status=value["status"], object=value["object"]) for name, value in record["components"].items()} if record else {},
                          problems=diagnostics))

    run = state["last_run"]
    return dict(corpus_id=identifier, checkpoint=token, repository=plan["repository"],
                inventory_snapshot=plan["inventory_snapshot"], scope=plan["scope"], profile=plan["profile"], max_age=plan["max_age"],
                status=state["status"], updated_at=state["updated_at"], declared_counts=counts,
                last_run=dict(started_at=run["started_at"], request_budget=run["request_budget"], requests=run["requests"], reason_present=run["reason"] is not None) if run else None,
                pagination=pagination, items=items,
                continuation=dict(offset=pagination["next_offset"], limit=limit, checkpoint=token) if pagination["next_offset"] is not None else None,
                verification="plan/state checksums and structure, inventory manifest membership, selected result manifests only; no payload or inventory provenance audit",
                coverage_basis="declared checkpoint outcomes, not verified payload availability or current backlog coverage",
                freshness_basis="selected recorded revisions and local age, not current GitHub state",
                requests=0)


def read(cache, snapshot_id, kind, number, component, offset=0, limit=20,
         byte_offset=0, max_bytes=16384, max_age=DEFAULT_MAX_AGE):
    window(offset, limit, 100)
    window(byte_offset, max_bytes, 65536)
    natural(max_age, "maximum age")
    if component not in COMPONENTS:
        raise ValueError("unsupported component")

    manifest = cache.load_manifest(snapshot_id)
    record = next((row for row in manifest["items"] if
                   (row["identity"]["kind"], row["identity"]["number"]) == (kind, number)), None)
    if record is None:
        raise ValueError("item is not in selected snapshot")

    descriptor = record["components"].get(component)
    result = dict(snapshot_id=snapshot_id, repository=manifest["repository"], identity=record["identity"],
                  revision=record["revision"], component=component, descriptor=descriptor,
                  problems=problems(record, [component], max_age), requests=0,
                  freshness_basis="recorded revision and local age, not current GitHub state",
                  verification="selected object only; other component payloads are not audited")
    if not descriptor or not descriptor["object"]:
        if offset or byte_offset:
            raise ValueError("missing component has no content offset")

        return dict(result, available=False, text=None, pagination=page(0, 0, 0), continuation=None)

    payload = cache.read_object(descriptor["object"])
    validate_payload(component, descriptor, kind, payload)
    if component == "diff":
        entries = payload.splitlines(keepends=True)
        encoding = "diff-lines"
    else:
        value = json.loads(payload)
        entries = value if isinstance(value, list) else [value]
        encoding = "json-array" if isinstance(value, list) else "json-value"

    selected = entries[offset:offset + limit]
    pagination = page(len(entries), offset, len(selected))
    text = "".join(selected) if component == "diff" else json.dumps(selected if encoding == "json-array" else (selected[0] if selected else None), ensure_ascii=False, separators=(",", ":"))
    raw = text.encode("utf-8")
    if byte_offset > len(raw):
        raise ValueError("byte offset exceeds selected page")

    try:
        raw[:byte_offset].decode("utf-8")
    except UnicodeDecodeError as error:
        raise ValueError("byte offset must be a UTF-8 boundary") from error

    end = min(len(raw), byte_offset + max_bytes)
    fragment = raw[byte_offset:end].decode("utf-8", errors="ignore")
    end = byte_offset + len(fragment.encode("utf-8"))
    if end == byte_offset and end < len(raw):
        raise ValueError("max-bytes is too small for the next UTF-8 character")

    continuation = None
    if end < len(raw):
        continuation = dict(offset=offset, limit=limit, byte_offset=end)
    elif pagination["next_offset"] is not None:
        continuation = dict(offset=pagination["next_offset"], limit=limit, byte_offset=0)

    return dict(result, available=True, encoding=encoding, text=fragment, pagination=pagination,
                bytes=page(len(raw), byte_offset, end - byte_offset), continuation=continuation,
                page_complete=byte_offset == 0 and end == len(raw),
                note="Concatenate byte fragments before parsing JSON. Offsets are zero-based; diff lines retain original endings. Reuse the exact snapshot, item, component and limit.")
