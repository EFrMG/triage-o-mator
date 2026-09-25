"""Offline evidence storage. Content-addressed objects are durable before an immutable manifest is published."""

import json
import os
from pathlib import Path

from _cache_index import CurrentIndex, item_key, order
from _evidence import VERSION, DIGEST, artifact_ref, canonical, coverage, fields, object_name, same_item, same_repository, validate_item, validate_payload, validate_repository, validate_snapshot, version
from _storage import atomic_writer, locked, sync_directory
from _triage import DATA_DIR

CACHE_DIR = DATA_DIR / "cache"
DATASET_LIMIT_BYTES = 5_000_000_000
DATASET_RESERVE_BYTES = 256_000_000


class CacheCapacityError(ValueError):
    pass


class EvidenceCache:
    def __init__(self, identity, root=CACHE_DIR):
        validate_repository(identity)
        self.identity = dict(identity)
        self.root = Path(root)
        self._usage_state = None

    def usage(self):
        """Count actual regular-file bytes without following symlinks or creating cache files."""
        totals = dict(objects=0, snapshots=0, other=0)
        allocated = dict(objects=0, snapshots=0, other=0)
        count = 0
        if self.root.exists():
            if self.root.is_symlink():
                raise ValueError("cache root must not be a symlink")

            pending = [(self.root, "other")]
            while pending:
                directory, category = pending.pop()
                with os.scandir(directory) as entries:
                    for entry in entries:
                        if entry.is_symlink():
                            raise ValueError("cache artifacts must not be symlinks")

                        group = entry.name if directory == self.root and entry.name in ("objects", "snapshots") else category
                        if entry.is_dir(follow_symlinks=False):
                            pending.append((entry.path, group))
                        elif entry.is_file(follow_symlinks=False):
                            stat = entry.stat(follow_symlinks=False)
                            totals[group] += stat.st_size
                            allocated[group] += stat.st_blocks * 512
                            count += 1

        return dict(total_bytes=sum(totals.values()), allocated_bytes=sum(allocated.values()), file_count=count, categories=totals,
                    allocated_categories=allocated, limit_bytes=DATASET_LIMIT_BYTES,
                    remaining_bytes=max(0, DATASET_LIMIT_BYTES - sum(allocated.values())))

    def _storage_signature(self):
        def stamp(name):
            path = self.path(name)
            if not path.exists():
                return None

            stat = path.stat()
            return stat.st_ino, stat.st_mtime_ns, stat.st_ctime_ns

        return tuple(stamp(name) for name in ("objects", "snapshots"))

    def _budget_used(self):
        signature = self._storage_signature()
        if self._usage_state is None or self._usage_state[0] != signature:
            self._usage_state = (signature, self.usage()["allocated_bytes"])

        return self._usage_state[1]

    def _check_capacity(self, additional):
        used = self._budget_used()
        if additional > 0 and used + additional + DATASET_RESERVE_BYTES > DATASET_LIMIT_BYTES:
            raise CacheCapacityError("local dataset storage limit (5 GB) reached; acquisition stopped with saved evidence intact")

        return used

    def _record_growth(self, used, additional):
        self._usage_state = (self._storage_signature(), used + additional)

    def _estimated_allocation(self, size):
        block = os.statvfs(self.root).f_frsize
        return ((size + block - 1) // block) * block

    def store_object(self, ref, payload):
        """Write a page-checkpoint object under the same capacity guard as published snapshots."""
        if artifact_ref(payload, ref["format"]) != ref:
            raise ValueError("supplied evidence checksum/size mismatch")

        with locked(self.path("cache.json")):
            self.require_ready(bound=True)
            self.path("objects").mkdir(exist_ok=True)
            sync_directory(self.root)
            path = self.object_path(ref)
            if path.exists():
                self.read_object(ref)
            else:
                used = self._check_capacity(self._estimated_allocation(len(payload.encode("utf-8"))))
                with atomic_writer(path) as out:
                    out.write(payload)

                self._record_growth(used, path.stat().st_blocks * 512)

    def path(self, *parts):
        path = self.root
        if path.is_symlink():
            raise ValueError("cache root must not be a symlink")

        for part in parts:
            path = path / part
            if path.is_symlink():
                raise ValueError("cache artifacts must not be symlinks")

        if (path.parent / "local").is_symlink():
            raise ValueError("cache local storage must not be a symlink")

        return path

    def metadata(self):
        path = self.path("cache.json")
        if not path.exists():
            return None

        value = json.loads(path.read_text(encoding="utf-8"))
        fields(value, ("schema_version", "artifact", "repository"))
        version(value, "evidence-cache")
        same_repository(self.identity, value["repository"])

        return value

    def initialize(self, dry_run=False):
        metadata = self.metadata()
        if metadata is not None:
            return dict(status="ready", metadata=metadata)

        # Refuse to adopt an unrelated directory; interrupted initialization only leaves our ignore file and local temporaries.
        if self.root.exists():
            if any(path.name not in (".gitignore", "local") for path in self.root.iterdir()):
                # A concurrent initializer may have committed after our first metadata read.
                metadata = self.metadata()
                if metadata is not None:
                    return dict(status="ready", metadata=metadata)

                raise ValueError("cache directory has unrecognized data without cache.json; preserve it and inspect before initializing")

            ignore = self.path(".gitignore")
            if ignore.exists() and ignore.read_text() != "*\n":
                raise ValueError("cache directory has a custom .gitignore; refusing to replace it")

        metadata = dict(schema_version=VERSION, artifact="evidence-cache", repository=self.identity)
        if dry_run:
            return dict(status="would-initialize", metadata=metadata)

        path = self.path("cache.json")
        with locked(path):
            existing = self.metadata()
            if existing is not None:
                return dict(status="ready", metadata=existing)

            with atomic_writer(self.path(".gitignore")) as out:
                out.write("*\n")

            with atomic_writer(path) as out:
                out.write(canonical(metadata) + "\n")

            sync_directory(self.root.parent)

        return dict(status="initialized", metadata=metadata)

    def bind_repository(self, observed):
        """Pin stable IDs from a later verified repository read; reject moves/case aliases instead of guessing a migration."""
        validate_repository(observed)
        if observed["database_id"] is None and observed["node_id"] is None:
            raise ValueError("binding needs a stable repository ID")

        if self.metadata() is None:
            raise ValueError("initialize the cache before binding its repository")

        with locked(self.path("cache.json")):
            metadata = self.metadata()
            same_repository(metadata["repository"], observed)
            metadata["repository"] = dict(observed)
            with atomic_writer(self.path("cache.json")) as out:
                out.write(canonical(metadata) + "\n")

        return metadata

    def require_ready(self, bound=False):
        metadata = self.metadata()
        if metadata is None:
            raise ValueError("cache is not initialized; run bin/cache init --dry-run first")

        if bound and metadata["repository"]["database_id"] is None and metadata["repository"]["node_id"] is None:
            raise ValueError("repository identity is unverified; bind stable IDs before using evidence")

        return metadata

    def object_path(self, ref):
        return self.path("objects", object_name(ref))

    def read_object(self, ref):
        payload = self.object_path(ref).read_bytes().decode("utf-8")
        if artifact_ref(payload, ref["format"]) != ref:
            raise ValueError("evidence object checksum/size mismatch")

        return payload

    def publish(self, manifest, payloads):
        """payloads maps object filenames to text. Missing entries must already exist and verify. No ledger changes."""
        validate_snapshot(manifest)
        metadata = self.require_ready(bound=True)
        same_repository(metadata["repository"], manifest["repository"])
        refs = {object_name(component["object"]): component["object"] for record in manifest["items"] for component in record["components"].values() if component["object"] is not None}
        if payloads.keys() - refs.keys():
            raise ValueError("payload is not referenced by the snapshot")

        # Validate all supplied bytes before creating any object. Partial acquisition belongs in explicit component statuses.
        for name, payload in payloads.items():
            if artifact_ref(payload, refs[name]["format"]) != refs[name]:
                raise ValueError("supplied evidence checksum/size mismatch")

        for record in manifest["items"]:
            for name, component in record["components"].items():
                if component["object"] is not None:
                    ref = component["object"]
                    filename = object_name(ref)
                    payload = payloads[filename] if filename in payloads else self.read_object(ref)
                    validate_payload(name, component, record["identity"]["kind"], payload)

        with locked(self.path("cache.json")):
            same_repository(self.require_ready(bound=True)["repository"], manifest["repository"])
            index = CurrentIndex(self).ensure()
            index.add(manifest)
            for directory in ("objects", "snapshots"):
                self.path(directory).mkdir(exist_ok=True)

            path = self.path("snapshots", manifest["snapshot_id"] + ".json")
            incoming_objects = [(name, ref) for name, ref in refs.items() if not self.object_path(ref).exists()]
            missing_bytes = sum(self._estimated_allocation(len(payloads[name].encode("utf-8"))) for name, _ in incoming_objects if name in payloads)
            if path.exists():
                manifest_bytes = 0
            else:
                manifest_bytes = self._estimated_allocation(len((canonical(manifest) + "\n").encode("utf-8")))

            used = self._check_capacity(missing_bytes + manifest_bytes)

            # Persist new directory entries before a manifest can reference objects beneath them.
            sync_directory(self.root)
            for name, ref in refs.items():
                object_path = self.object_path(ref)
                if object_path.exists():
                    self.read_object(ref)
                elif name in payloads:
                    with atomic_writer(object_path) as out:
                        out.write(payloads[name])
                else:
                    raise ValueError(f"missing evidence object: {name}")

            if path.exists():
                if self.load(manifest["snapshot_id"]) != manifest:
                    raise ValueError("immutable snapshot already exists with different content")
            else:
                # A durable invalidation precedes the snapshot commit. An interrupted index update must never hide a newer partial observation.
                index.invalidate()
                # The single manifest is the commit record: never publish it before all referenced objects have been flushed.
                with atomic_writer(path) as out:
                    out.write(canonical(manifest) + "\n")

                index.snapshot_count += 1

            old_index_bytes = index.path.stat().st_blocks * 512 if index.path.exists() else 0
            index.commit()
            self._record_growth(used, missing_bytes + manifest_bytes + index.path.stat().st_blocks * 512 - old_index_bytes)

        return manifest["snapshot_id"]

    def load_manifest(self, snapshot_id):
        """Verify manifest identity and descriptors only; callers must verify any selected objects separately."""
        if not isinstance(snapshot_id, str) or not DIGEST.fullmatch(snapshot_id):
            raise ValueError("snapshot ID must be a SHA-256 digest")

        metadata = self.require_ready(bound=True)
        manifest = json.loads(self.path("snapshots", snapshot_id + ".json").read_text(encoding="utf-8"))
        validate_snapshot(manifest)
        if manifest["snapshot_id"] != snapshot_id:
            raise ValueError("snapshot filename/identity mismatch")

        same_repository(metadata["repository"], manifest["repository"])
        return manifest

    def load(self, snapshot_id):
        manifest = self.load_manifest(snapshot_id)
        for record in manifest["items"]:
            for name, component in record["components"].items():
                if component["object"] is not None:
                    payload = self.read_object(component["object"])
                    validate_payload(name, component, record["identity"]["kind"], payload)

        return manifest

    def status(self):
        metadata = self.metadata()
        if metadata is None:
            return dict(status="not-initialized", repository=self.identity, snapshots=[])

        snapshots = []
        for path in sorted(self.path("snapshots").glob("*.json")):
            snapshots.append(dict(snapshot_id=path.stem, coverage=coverage(self.load(path.stem))))

        bound = metadata["repository"]["database_id"] is not None or metadata["repository"]["node_id"] is not None

        return dict(status="ready", repository=metadata["repository"], identity_bound=bound, snapshots=snapshots)

    def latest(self, kind, number):
        """Select through a rebuildable index, then verify the chosen immutable snapshot and its objects."""
        identity = dict(kind=kind, number=number, database_id=None, node_id=None)
        validate_item(identity)
        if self.metadata() is None:
            return None

        with locked(self.path("cache.json")):
            index = CurrentIndex(self).ensure()
            entry = index.entries.get(item_key(identity))
            if entry is None:
                return None

            manifest = self.load(entry["snapshot_id"])
            record = next((row for row in manifest["items"] if item_key(row["identity"]) == item_key(identity)), None)
            if record is None or order(entry) != order(manifest):
                raise ValueError("current-item index does not match its snapshot; run bin/cache rebuild-index")

            # Known IDs may come from older observations; unknown IDs in the selected snapshot are not invented in returned evidence.
            same_item(record["identity"], entry["identity"])

            return manifest, record

    def rebuild_index(self):
        self.require_ready()
        with locked(self.path("cache.json")):
            index = CurrentIndex(self).rebuild()

            return dict(status="rebuilt", repository=index.repository, snapshots=index.snapshot_count, items=len(index.entries))
