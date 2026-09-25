"""Local presentation state for retained watch and imported-action rows."""

from _evidence import canonical, digest, natural, same_repository
from _jobs import read_record
from _storage import atomic_writer, locked
from _triage import DATA_DIR


def path_for():
    return DATA_DIR / "local" / "notification-state.json"


def load(cache):
    path = path_for()
    if not path.exists():
        return dict(schema_version=1, artifact="notification-state", repository=cache.identity, rows={})

    value = read_record(path, "notification-state", ("repository", "rows"))
    same_repository(cache.identity, value["repository"])
    if not isinstance(value["rows"], dict):
        raise ValueError("invalid notification state")

    for key, row in value["rows"].items():
        if not isinstance(key, str) or not key.startswith(("watch:pr:", "action:pr:")) or not key.rsplit(":", 1)[1].isdigit() or int(key.rsplit(":", 1)[1]) < 1:
            raise ValueError("invalid notification key")

        checkpoint = row.get("viewed_checkpoint") if isinstance(row, dict) else None
        if not isinstance(row, dict) or set(row) != {"viewed_checkpoint", "dismissed"} or type(row["dismissed"]) is not bool:
            raise ValueError("invalid notification row")
        if checkpoint is not None and (not isinstance(checkpoint, str) or len(checkpoint) != 64 or any(ch not in "0123456789abcdef" for ch in checkpoint)):
            raise ValueError("invalid notification row")

    return value


def listing(cache):
    value = load(cache)
    return dict(repository=value["repository"], rows=value["rows"], requests=0)


def change(cache, source, number, action, checkpoint):
    natural(number, "notification number", 1)
    if source not in ("watch", "action") or action not in ("view", "dismiss") or not isinstance(checkpoint, str) or len(checkpoint) != 64 or any(ch not in "0123456789abcdef" for ch in checkpoint):
        raise ValueError("invalid notification change")

    if source == "watch":
        from _attention import catalog
        from _watch import load as load_record
    else:
        from _action_history import catalog
        from _closure_store import load as load_record

    try:
        current = load_record(cache, number)["checksum"]
    except (ValueError, OSError, KeyError, TypeError):
        current = None
    if current != checkpoint:
        _, members, token = catalog(cache)
        if action != "dismiss" or current is not None or token != checkpoint or number not in (member for member, _ in members):
            raise ValueError("notification changed; reopen Notifications")

    path = path_for()
    with locked(path):
        value = load(cache)
        key = f"{source}:pr:{number}"
        row = value["rows"].setdefault(key, dict(viewed_checkpoint=None, dismissed=False))
        if action == "view":
            row["viewed_checkpoint"] = checkpoint
        else:
            row["dismissed"] = True
        value.pop("checksum", None)
        value["checksum"] = digest(canonical(value))
        with atomic_writer(path) as out:
            out.write(canonical(value) + "\n")

    return dict(source=source, number=number, action=action)
