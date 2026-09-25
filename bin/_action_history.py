"""Bounded offline navigation of imported external actions and their separately bound watch context."""

import re
import stat

from _chunks import page, window
from _closure_store import audit, load, watch_link
from _evidence import canonical, digest, natural

POLICY = "action-history-reader-v1"
PREVIEW_BYTES = 1200


def clipped(value, maximum):
    raw = value.encode("utf-8")
    preview = raw[:maximum].decode("utf-8", errors="ignore")

    return preview, len(raw) - len(preview.encode("utf-8"))


def row(identifier, label, value, **extra):
    preview, omitted = clipped(canonical(value), PREVIEW_BYTES)
    label, label_omitted = clipped(label, 240)

    return dict(id=identifier, label=label, preview=preview, omitted_bytes=omitted,
                label_omitted_bytes=label_omitted, **extra)


def envelope(repository, section, number, checkpoint, rows, offset, limit, **extra):
    chosen = rows[offset:offset + limit]

    return dict(schema_version=1, policy=POLICY, repository=repository, section=section, number=number,
                checkpoint=checkpoint, requests=0, rows=chosen, pagination=page(len(rows), offset, len(chosen)),
                meaning="Imported explanations and source observations are attributed history, not approval. Watch/case links are separate and do not change holds.",
                scope="Output text and page count are bounded; input history and selected evidence validation may read whole files.", **extra)


def catalog(cache):
    metadata = cache.metadata()
    repository = metadata["repository"] if metadata else cache.identity
    directory = cache.root.parent / "external-closures"
    members = []
    if directory.exists():
        if directory.is_symlink():
            raise ValueError("closure catalog must not use symlinks")
        for path in directory.iterdir():
            if path.name == "local":
                continue
            if not re.fullmatch(r"pr-[1-9][0-9]*\.json", path.name):
                raise ValueError("unrecognized closure filename; inspect local storage")
            try:
                checksum = digest(path.read_bytes().decode("utf-8")) if stat.S_ISREG(path.lstat().st_mode) else "not-regular"
            except (OSError, UnicodeError):
                checksum = "unreadable"
            members.append((int(path.name[3:-5]), checksum))

    members.sort()

    return repository, members, digest(canonical(dict(policy=POLICY, repository=repository, members=members)))


def unchanged(cache, number, checkpoint):
    if load(cache, number)["checksum"] != checkpoint:
        raise ValueError("action history changed during read; restart")


def listing(cache, offset=0, limit=10, checkpoint=None):
    window(offset, limit, 50)
    if offset and checkpoint is None:
        raise ValueError("action catalog continuation requires checkpoint")

    repository, members, token = catalog(cache)
    if checkpoint is not None and checkpoint != token:
        raise ValueError("action catalog changed; restart")
    rows = []
    for number, checksum in members[offset:offset + limit]:
        try:
            if checksum in ("unreadable", "not-regular"):
                raise ValueError("unavailable history")
            history = load(cache, number)
            rows.append(row(str(number), f"PR #{number}: {len(history['entries'])} imported action entries",
                            dict(entries=len(history["entries"]), latest_kind=history["entries"][-1]["kind"] if history["entries"] else None),
                            number=number, selectable=True, history_checkpoint=history["checksum"]))
        except (OSError, ValueError, KeyError, TypeError):
            rows.append(row(str(number), f"PR #{number}: unavailable action history", dict(problem="Inspect local storage; no live fallback"),
                            number=number, selectable=False, history_checkpoint=None))

    if catalog(cache)[2] != token:
        raise ValueError("action catalog changed during read; restart")
    result = envelope(repository, "list", 0, token, rows, 0, limit)
    result["pagination"] = page(len(members), offset, len(rows))

    return result


def bound(cache, number, checkpoint):
    if checkpoint is None:
        raise ValueError("action details require selected history checkpoint")
    history = load(cache, number)
    if history["checksum"] != checkpoint:
        raise ValueError("action history changed; restart from list")

    return history


def read_page(cache, number, section="context", offset=0, limit=10, checkpoint=None, entry=None):
    window(offset, limit, 50)
    if section not in ("context", "entries", "sources"):
        raise ValueError("unsupported action section")
    if (section == "sources") != (entry is not None):
        raise ValueError("sources require an entry; other sections do not")
    history = bound(cache, number, checkpoint)
    if section == "context":
        link = watch_link(cache, history)
        public_link = {key: link[key] for key in ("status", "checksum", "scope") if key in link}
        rows = [row("identity", "Bound repository and PR identity", dict(repository=history["repository"], item=history["item"])),
                row("watch", "Separate watch/case linkage", public_link, watch=public_link)]
    elif section == "entries":
        rows = [row(value["id"], f"{value['at']}: {value['kind']} by {value['by']}",
                    dict(kind=value["kind"], by=value["by"], at=value["at"], reason=value["reason"],
                         predecessor=value["predecessor"], claim=value["claim"], observation=value["observation"],
                         source_count=len(value["observation"]["sources"]))) for value in history["entries"]]
    else:
        selected = next((value for value in history["entries"] if value["id"] == entry), None)
        if selected is None:
            raise ValueError("entry is not in retained action history")
        rows = [row(str(index), f"{source['component']} · source {source['source_id'] if source['source_id'] is not None else 'summary'}",
                    source, reference=index) for index, source in enumerate(selected["observation"]["sources"])]

    unchanged(cache, number, checkpoint)

    return envelope(history["repository"], section, number, checkpoint, rows, offset, limit, entry=entry)


def source(cache, number, entry, reference=0, checkpoint=None, byte_offset=0, max_bytes=4096):
    if type(reference) is not int:
        raise ValueError("source reference must be an integer")

    if reference != -1:
        natural(reference, "source reference")
    window(byte_offset, max_bytes, 16384)
    history = bound(cache, number, checkpoint)
    selected = next((value for value in history["entries"] if value["id"] == entry), None)
    if selected is None or reference >= len(selected["observation"]["sources"]):
        raise ValueError("source reference is not in retained action history")
    if reference == -1:
        payload = selected
    else:
        result = audit(cache, history, selected["observation"])
        payload = result["sources"][reference]
    raw = canonical(payload).encode("utf-8")
    if byte_offset > len(raw):
        raise ValueError("byte offset exceeds source")
    try:
        raw[:byte_offset].decode("utf-8")
    except UnicodeDecodeError as error:
        raise ValueError("byte offset must be a UTF-8 boundary") from error
    fragment = raw[byte_offset:byte_offset + max_bytes].decode("utf-8", errors="ignore")
    if not fragment and byte_offset < len(raw):
        raise ValueError("max-bytes cannot fit next UTF-8 character")
    unchanged(cache, number, checkpoint)

    return dict(schema_version=1, policy=POLICY, repository=history["repository"], section="source", number=number,
                checkpoint=checkpoint, entry=entry, reference=reference, requests=0, rows=[], text=fragment,
                bytes=page(len(raw), byte_offset, len(fragment.encode("utf-8"))),
                audit_scope="record binding only; evidence payloads unaudited" if reference == -1 else "selected immutable component payloads; no live freshness or provenance authentication",
                meaning="Retained attributed entry; reading does not acknowledge or approve" if reference == -1 else "Untrusted pinned source fragment; reading does not acknowledge or approve.")
