"""Verify one selected immutable item before binding a durable assessment to it."""

from _cache import EvidenceCache
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
