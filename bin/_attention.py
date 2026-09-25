"""Bounded offline attention pages; tokens bind watched state, never authority or read completion."""

import json
import re
import stat

from _chunks import page, window
from _evidence import canonical, digest, natural, timestamp
from _watch import load, observation, selected
from _watch_history import collect

POLICY = "attention-reader-v1"
SECTIONS = ("context", "history", "references")
PREVIEW_BYTES = 1200


def clipped(value, maximum):
    raw = value.encode("utf-8")
    text = raw[:maximum].decode("utf-8", errors="ignore")

    return text, len(raw) - len(text.encode("utf-8"))


def row(identifier, label, payload, **extra):
    preview, omitted = clipped(canonical(payload), PREVIEW_BYTES)
    label, label_omitted = clipped(label, 240)

    return dict(id=identifier, label=label, preview=preview, omitted_bytes=omitted,
                label_omitted_bytes=label_omitted, **extra)


def envelope(repository, section, number, token, rows, offset, limit, **extra):
    chosen = rows[offset:offset + limit]
    return dict(schema_version=1, policy=POLICY, repository=repository, section=section, number=number,
                checkpoint=token, requests=0, rows=chosen, pagination=page(len(rows), offset, len(chosen)),
                meaning="Observed activity is not a confirmed appeal. Reading does not acknowledge, approve or fetch.",
                scope="Output counts and text bytes are bounded; retained evidence verification and input memory are not.", **extra)


def catalog(cache):
    metadata = cache.metadata()
    repository = metadata["repository"] if metadata else cache.identity
    directory = cache.path("watches")
    members = []
    if metadata and directory.exists():
        for path in directory.iterdir():
            if path.name == "local":
                continue
            if not re.fullmatch(r"pr-[1-9][0-9]*\.json", path.name):
                raise ValueError("unrecognized watch filename; inspect cache storage")

            if stat.S_ISREG(path.lstat().st_mode):
                try:
                    checksum = digest(path.read_text(encoding="utf-8"))
                except (OSError, UnicodeError):
                    checksum = "unreadable"
            else:
                checksum = "not-regular"
            members.append((int(path.name[3:-5]), checksum))

    members.sort()
    token = digest(canonical(dict(policy=POLICY, repository=repository, members=members)))

    return repository, members, token


def bound(cache, number, checkpoint=None):
    watch = load(cache, number)
    if checkpoint is not None and checkpoint != watch["checksum"]:
        raise ValueError("attention watch changed; restart from the watch list")

    entries, coverage = collect(cache, watch)

    return watch, dict(entries=entries, coverage=coverage, responses=responses(entries))


def responses(entries):
    """Saved comment revisions observed at or after the recorded closure. A count of leads to read, never a classified appeal."""
    return sum(entry["relation"] != "before-closure" and entry["source_id"].startswith("comment:") for entry in entries)


def unchanged(cache, watch):
    if load(cache, watch["item"]["number"])["checksum"] != watch["checksum"]:
        raise ValueError("attention watch changed during read; restart")


def listing(cache, offset=0, limit=10, checkpoint=None):
    window(offset, limit, 50)
    if offset and checkpoint is None:
        raise ValueError("attention continuation requires --checkpoint")

    repository, members, token = catalog(cache)
    if checkpoint is not None and checkpoint != token:
        raise ValueError("attention catalog or watch progress changed; restart")

    rows = []
    for number, checksum in members[offset:offset + limit]:
        try:
            if checksum in ("unreadable", "not-regular"):
                raise ValueError("unavailable watch")
            watch, state = bound(cache, number)
            poll = watch.get("poll") or {}
            operational = poll.get("status") in ("error", "partial", "running") or not state["coverage"]["latest_complete"]
            reasons = []
            if state["responses"]:
                reasons.append("activity after closure")
            if operational:
                reasons.append("operational / coverage attention")
            unchanged(cache, watch)
            rows.append(row(str(number), f"PR #{number}: " + (", ".join(reasons) or "observation only"),
                            dict(poll_status=poll.get("status"), coverage=state["coverage"], response_comments=state["responses"]),
                            number=number, selectable=True, watch_checkpoint=watch["checksum"], attention=bool(reasons)))
        except (ValueError, OSError, TypeError, KeyError):
            rows.append(row(str(number), f"PR #{number}: unavailable evidence / watch", dict(problem="Retained watch or evidence unavailable; inspect local storage. No live fallback."),
                            number=number, selectable=False, watch_checkpoint=None, attention=True))

    if catalog(cache)[2] != token:
        raise ValueError("attention catalog changed during read; restart")

    result = envelope(repository, "list", 0, token, rows, 0, limit)
    result["pagination"] = page(len(members), offset, len(rows))
    return result


def documents(cache, watch, state, section):
    if section == "context":
        latest = observation(cache, watch, watch["observations"][-1])
        _, _, summary = selected(cache, watch["observations"][-1], watch["item"]["number"])
        _, _, original = selected(cache, watch["observations"][0], watch["item"]["number"])
        values = [
            ("closure", "Original closure references / supplied rationale provenance", dict(closure=watch["closure"], observed_closed_at=watch["observed_closed_at"], provenance=watch["provenance"])),
            ("survivor", "Supplied survivor (not independently checked)", dict(number=watch["survivor"], repository=watch["repository"]["full_name"],
                url=f"https://{watch['repository']['host']}/{watch['repository']['full_name']}/pull/{watch['survivor']}" if watch["survivor"] else None)),
            ("coverage", "Coverage and last successful check", dict(coverage=state["coverage"], latest_gaps=latest["gaps"], latest_observed_state=latest["state"])),
            ("poll", "Operational polling status / failure", watch.get("poll")),
            ("summary", "Latest observed PR summary / body", summary),
            ("original-summary", "Enrollment PR summary / retained original body", original),
        ]
        return [(identifier, label, value) for identifier, label, value in values]

    entries = list(state["entries"])

    def chronology(entry):
        dates = []
        for value in entry["source_times"].values():
            if value is not None:
                try:
                    dates.append(timestamp(value))
                except ValueError:
                    pass

        return (min(dates).timestamp() if dates else float("inf"), entry["first_observed"], entry["id"])

    # Source timestamps order the discussion, not proof that missing historical events never existed. Unknown dates sort last.
    entries.sort(key=chronology)
    return entries


def history_preview(watch, entry, source=None):
    value = {key: val for key, val in entry.items() if key != "references"}
    value["reference_count"] = len(entry["references"])
    value["supplied_closure_reference"] = entry["source_id"] == f"comment:{watch['closure']['comment_id']}" or (
        entry["source_id"].startswith("timeline:") and entry["source_id"].endswith(f":id:{watch['closure']['event_id']}"))
    if isinstance(source, dict) and entry["source_id"].startswith("comment:"):
        body = source.get("body")
        user = source.get("user")
        value["comment_author"] = clipped(user.get("login"), 80)[0] if isinstance(user, dict) and isinstance(user.get("login"), str) else None
        if isinstance(body, str):
            value["comment_excerpt"], value["comment_excerpt_omitted_bytes"] = clipped(body, 240)
    return value


def read_page(cache, number, section="context", offset=0, limit=10, checkpoint=None, entry=None):
    window(offset, limit, 50)
    if section not in SECTIONS:
        raise ValueError("unsupported attention section")
    if checkpoint is None:
        raise ValueError("attention details require the selected watch --checkpoint")
    if section != "references" and entry is not None:
        raise ValueError("entry is only valid for reference pages")

    watch, state = bound(cache, number, checkpoint)
    docs = documents(cache, watch, state, section)
    if section == "context":
        rows = [row(identifier, label, value) for identifier, label, value in docs]
    elif section == "history":
        observations = {}
        rows = []
        for index, value in enumerate(docs):
            source = None
            if offset <= index < offset + limit:
                ref = value["references"][0]
                snapshot = ref["snapshot_id"]
                if snapshot not in observations:
                    observations[snapshot] = observation(cache, watch, snapshot)
                activity = next((item for item in observations[snapshot]["activity"] if item["component"] == ref["component"] and item["source_offset"] == ref["offset"]), None)
                if activity is None or digest(canonical(activity["source"])) != ref["source_digest"]:
                    raise ValueError("attention source reference changed")
                source = activity["source"]
            when = value["source_times"].get("created_at") or value["source_times"].get("submitted_at") or "time unknown"
            rows.append(row(value["id"], value["source_id"] + " · " + str(when), history_preview(watch, value, source)))
    else:
        selected_entry = next((value for value in docs if value["id"] == entry), None)
        if selected_entry is None:
            raise ValueError("source revision is not in this watch")
        rows = [row(str(index), f"{ref['observed_at']}: {ref['component']}", ref, reference=index)
                for index, ref in enumerate(selected_entry["references"])]

    unchanged(cache, watch)
    return envelope(watch["repository"], section, number, checkpoint, rows, offset, limit, entry=entry)


def source(cache, number, section, entry, reference=0, checkpoint=None, byte_offset=0, max_bytes=4096):
    window(byte_offset, max_bytes, 16384)
    natural(reference, "reference index")
    if checkpoint is None or section not in ("context", "history"):
        raise ValueError("attention source requires checkpoint and a source section")
    if section != "history" and reference:
        raise ValueError("only history sources have multiple references")

    watch, state = bound(cache, number, checkpoint)
    docs = documents(cache, watch, state, section)
    if section == "history":
        selected_entry = next((value for value in docs if value["id"] == entry), None)
        if selected_entry is None or reference >= len(selected_entry["references"]):
            raise ValueError("source revision/reference is not in this watch")
        ref = selected_entry["references"][reference]
        manifest = cache.load(ref["snapshot_id"])
        record = next(record for record in manifest["items"] if record["identity"] == watch["item"])
        payload = json.loads(cache.read_object(record["components"][ref["component"]]["object"]))[ref["offset"]]
        if digest(canonical(payload)) != ref["source_digest"]:
            raise ValueError("source reference digest mismatch")
    else:
        selected_doc = next((value for identifier, _, value in docs if identifier == entry), None)
        if not any(identifier == entry for identifier, _, _ in docs):
            raise ValueError("source entry is not in this watch section")
        payload = selected_doc

    raw = canonical(payload).encode("utf-8")
    if byte_offset > len(raw):
        raise ValueError("byte offset exceeds source")
    try:
        raw[:byte_offset].decode("utf-8")
    except UnicodeDecodeError as error:
        raise ValueError("byte offset must be a UTF-8 boundary") from error
    text = raw[byte_offset:byte_offset + max_bytes].decode("utf-8", errors="ignore")
    if not text and byte_offset < len(raw):
        raise ValueError("max-bytes cannot fit the next UTF-8 character")

    unchanged(cache, watch)
    return dict(schema_version=1, policy=POLICY, repository=watch["repository"], number=number, section="source",
                source_section=section, entry=entry, reference=reference, checkpoint=checkpoint, requests=0,
                text=text, bytes=page(len(raw), byte_offset, len(text.encode("utf-8"))),
                meaning="Untrusted pinned source fragment; concatenate bytes before parsing. Not a read-completion receipt.")
