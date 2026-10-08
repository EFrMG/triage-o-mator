"""Verify one selected immutable item before binding a durable assessment to it."""

from _cache import EvidenceCache
from _cache_index import CurrentIndex
from _evidence import repository, validate_payload
from _triage import REPO


def selected_item(kind, number, snapshot_id, host="github.com"):
    cache = EvidenceCache(repository(REPO, host=host))
    manifest = cache.load_manifest(snapshot_id)
    record = next((item for item in manifest["items"] if (item["identity"]["kind"], item["identity"]["number"]) == (kind, number)), None)
    if record is None:
        raise ValueError(f"{kind}:{number} is not in selected snapshot")

    for name, component in record["components"].items():
        if component["object"] is not None:
            validate_payload(name, component, kind, cache.read_object(component["object"]))

    return dict(snapshot_id=snapshot_id, repository=manifest["repository"], identity=record["identity"],
                revision=record["revision"], coverage={name: component["status"] for name, component in record["components"].items()})


# What a numeric score needs complete, per docs/item-score.md.
SCORE_CORE = {"issue": ("summary",), "pr": ("summary", "files", "diff")}


def unassessed_with_new_evidence(rows, host="github.com"):
    """Keys of unassessed results whose item has since gained a newer snapshot with its core evidence complete, so the scoring queue offers them again.

    Reads the current-item index as it stands, without rebuilding it, and each candidate's manifest. An absent cache, an index that needs rebuilding or an unreadable manifest yields no key. This is a queue lead, not evidence: scoring still verifies the snapshot it selects.
    """
    waiting = [row for row in rows if isinstance(row.get("item_score"), dict) and row["item_score"].get("value") is None]
    if not waiting:
        return set()

    try:
        cache = EvidenceCache(repository(REPO, host=host))
        index = CurrentIndex(cache)
        if not index.path.exists() or index.pending.exists() or index.read() != index.history_stamp():
            return set()
    except (OSError, ValueError, TypeError, KeyError):
        return set()

    due = set()
    for row in waiting:
        key = (row["kind"], row["number"])
        entry = index.entries.get(f"{key[0]}:{key[1]}")
        if entry is None or entry["snapshot_id"] == row["item_score"].get("snapshot_id"):
            continue

        try:
            manifest = cache.load_manifest(entry["snapshot_id"])
        except (OSError, ValueError, TypeError, KeyError):
            continue

        record = next((item for item in manifest["items"] if (item["identity"]["kind"], item["identity"]["number"]) == key), None)
        if record and all((record["components"].get(name) or {}).get("status") == "complete" for name in SCORE_CORE[key[0]]):
            due.add(key)

    return due
