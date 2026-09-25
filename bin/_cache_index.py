"""Rebuildable current-item pointers. Callers hold the cache metadata lock, never a separate index lock."""

import json

from _evidence import DIGEST, canonical, digest, fields, natural, same_repository, timestamp, validate_item, version
from _storage import atomic_writer, sync_directory


def item_key(identity):
    return f"{identity['kind']}:{identity['number']}"


def order(entry):
    return timestamp(entry["completed_at"]), timestamp(entry["started_at"]), entry["snapshot_id"]


class CurrentIndex:
    def __init__(self, cache):
        self.cache = cache
        self.path = cache.path("indexes", "current.json")
        self.pending = cache.path("indexes", "current.pending")
        self.repository = cache.require_ready()["repository"]
        self.entries = {}
        self.owners = {}
        self.snapshot_count = 0

    def history_stamp(self):
        path = self.cache.path("snapshots")
        if not path.exists():
            return None

        stat = path.stat()

        return dict(device=stat.st_dev, inode=stat.st_ino, mtime_ns=stat.st_mtime_ns, ctime_ns=stat.st_ctime_ns)

    def remember(self, key, identity):
        for field in ("database_id", "node_id"):
            value = identity[field]
            if value is None:
                continue

            identifier = (field, identity["kind"] if field == "database_id" else None, value)
            previous = self.owners.get(identifier)
            if previous is not None and previous != key:
                raise ValueError("stable item ID appears under multiple numbers/kinds in cache history")

            self.owners[identifier] = key

    def add(self, manifest):
        for record in manifest["items"]:
            identity = dict(record["identity"])
            key = item_key(identity)
            previous = self.entries.get(key)
            if previous:
                # Historical observations may predate stable-ID discovery. Retain every known ID and reject contradictions regardless of publication order.
                for field in ("database_id", "node_id"):
                    known = previous["identity"][field]
                    if known is not None:
                        if identity[field] is not None and identity[field] != known:
                            raise ValueError(f"item {field} mismatch in cache history")

                        identity[field] = known

            self.remember(key, identity)
            entry = dict(identity=identity, snapshot_id=manifest["snapshot_id"], started_at=manifest["started_at"], completed_at=manifest["completed_at"])
            if previous and order(previous) > order(entry):
                entry = dict(previous, identity=identity)

            self.entries[key] = entry

    def read(self):
        value = json.loads(self.path.read_text(encoding="utf-8"))
        fields(value, ("schema_version", "artifact", "repository", "history_stamp", "snapshot_count", "items", "checksum"))
        version(value, "evidence-current-index")
        same_repository(value["repository"], self.repository)
        if value["checksum"] != digest(canonical({key: item for key, item in value.items() if key != "checksum"})):
            raise ValueError("current-item index checksum mismatch")

        stamp = value["history_stamp"]
        if stamp is not None:
            fields(stamp, ("device", "inode", "mtime_ns", "ctime_ns"))
            for name, number in stamp.items():
                natural(number, name)

        natural(value["snapshot_count"], "snapshot_count")
        if not isinstance(value["items"], dict):
            raise ValueError("index items must be a mapping")

        if bool(value["items"]) != bool(value["snapshot_count"]):
            raise ValueError("index snapshot count and item presence disagree")

        for key, entry in value["items"].items():
            fields(entry, ("identity", "snapshot_id", "started_at", "completed_at"))
            validate_item(entry["identity"])
            if key != item_key(entry["identity"]):
                raise ValueError("index item key/identity mismatch")

            if not isinstance(entry["snapshot_id"], str) or not DIGEST.fullmatch(entry["snapshot_id"]):
                raise ValueError("invalid index snapshot ID")

            if timestamp(entry["started_at"]) > timestamp(entry["completed_at"]):
                raise ValueError("invalid index observation interval")

            self.remember(key, entry["identity"])

        self.entries = value["items"]
        self.snapshot_count = value["snapshot_count"]

        return stamp

    def ensure(self):
        # Invalid indexes require explicit recovery, even if a previous writer left a pending marker.
        if self.path.exists():
            try:
                stamp = self.read()
            except (ValueError, TypeError, KeyError) as error:
                raise ValueError(f"invalid current-item index: {error}; run bin/cache rebuild-index (with the same --host)") from error

            if not self.pending.exists() and stamp == self.history_stamp():
                return self

        return self.rebuild()

    def invalidate(self):
        self.path.parent.mkdir(exist_ok=True)
        sync_directory(self.cache.root)
        with atomic_writer(self.pending) as out:
            out.write("rebuild from immutable snapshots\n")

    def commit(self):
        value = dict(schema_version=1, artifact="evidence-current-index", repository=self.repository, history_stamp=self.history_stamp(), snapshot_count=self.snapshot_count, items=self.entries)
        value["checksum"] = digest(canonical(value))
        self.path.parent.mkdir(exist_ok=True)
        sync_directory(self.cache.root)
        with atomic_writer(self.path) as out:
            out.write(canonical(value) + "\n")

        if self.pending.exists():
            self.pending.unlink()
            sync_directory(self.path.parent)

    def rebuild(self):
        # Explicit recovery may replace corrupt derived bytes, but must not downgrade a recognizable future schema or adopt another repository's index.
        if self.path.exists():
            try:
                previous = json.loads(self.path.read_text(encoding="utf-8"))
            except (ValueError, UnicodeError):
                previous = None

            if isinstance(previous, dict):
                if "schema_version" in previous or "artifact" in previous:
                    version(previous, "evidence-current-index")

                if "repository" in previous:
                    same_repository(previous["repository"], self.repository)

        self.entries, self.owners = {}, {}
        self.snapshot_count = 0
        before = self.history_stamp()
        for path in sorted(self.cache.path("snapshots").glob("*.json")):
            self.add(self.cache.load(path.stem))
            self.snapshot_count += 1

        if before != self.history_stamp():
            raise ValueError("cache history changed outside its lock during rebuild; stop other writers and retry")

        self.commit()

        return self
