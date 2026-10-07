"""Content-addressed, tracked records for bounded playbook assessments."""

import json
import re

from _evidence import canonical, digest
from _storage import atomic_writer, locked
from _triage import REPO, now_iso


MAX_RECORDS = 5000


def save(directory, artifact, content, fields, max_bytes):
    if directory.is_symlink():
        raise ValueError(f"invalid {artifact} directory")

    assessment_id = digest(canonical(content))
    path = directory / f"{assessment_id}.json"
    with locked(directory):
        if path.exists() or path.is_symlink():
            previous = read(directory, artifact, assessment_id, fields, max_bytes)
            if all(previous.get(field) == content[field] for field in fields):
                return previous

            raise ValueError(f"{artifact} ID has conflicting contents")

        record = dict(schema_version=1, artifact=artifact, assessment_id=assessment_id, assessed_at=now_iso(), **content)
        record["checksum"] = digest(canonical(record))
        directory.mkdir(parents=True, exist_ok=True)
        with atomic_writer(path) as output:
            output.write(canonical(record) + "\n")

    return record


def read(directory, artifact, assessment_id, fields, max_bytes):
    if not re.fullmatch(r"[0-9a-f]{64}", assessment_id):
        raise ValueError(f"invalid {artifact} ID")

    path = directory / f"{assessment_id}.json"
    if directory.is_symlink() or path.is_symlink() or not path.is_file() or path.stat().st_size > max_bytes:
        raise ValueError(f"{artifact} is missing or invalid")

    record = json.loads(path.read_text(encoding="utf-8"))
    checksum = record.pop("checksum", None)
    content = {field: record[field] for field in fields}
    if (record.get("schema_version") != 1 or record.get("artifact") != artifact or record.get("repository") != REPO or
            record.get("assessment_id") != assessment_id or digest(canonical(content)) != assessment_id or digest(canonical(record)) != checksum):
        raise ValueError(f"{artifact} identity or contents changed")

    record["checksum"] = checksum
    return record


def list_records(directory, artifact, fields, limit, max_bytes):
    if not 1 <= limit <= MAX_RECORDS:
        raise ValueError(f"limit must be between 1 and {MAX_RECORDS}")
    if not directory.exists():
        return []
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError(f"invalid {artifact} directory")

    paths = sorted(directory.glob("*.json"))
    if len(paths) > MAX_RECORDS:
        raise ValueError(f"too many {artifact} records")

    records = [read(directory, artifact, path.stem, fields, max_bytes) for path in paths]
    records.sort(key=lambda row: (row["assessed_at"], row["assessment_id"]), reverse=True)
    return records[:limit]
