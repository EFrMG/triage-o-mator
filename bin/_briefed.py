"""Repository-scoped checkpoints for whole batches screened into maintainer briefs."""

import fcntl
import json
import re
from contextlib import contextmanager
from pathlib import Path

from _storage import atomic_writer
from _triage import DATA_DIR, REPO, REPORTS_DIR, WORK_ROOT, now_iso


BRIEFED_DIR = DATA_DIR / "briefed-batches"
BATCH_ID_RE = re.compile(r"^b\d{8}-\d{6}(-\d+)?$")


def batch_path(batch_id):
    if not isinstance(batch_id, str) or not BATCH_ID_RE.fullmatch(batch_id):
        raise ValueError(f"{batch_id!r} is not a batch ID")

    return BRIEFED_DIR / f"{batch_id}.json"


@contextmanager
def locked():
    BRIEFED_DIR.mkdir(parents=True, exist_ok=True)
    lock_path = DATA_DIR / "local" / "briefed-batches.lock"
    lock_path.parent.mkdir(parents=True, exist_ok=True)
    with lock_path.open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        yield


def load_records():
    records = {}
    for path in sorted(BRIEFED_DIR.glob("*.json")):
        record = json.loads(path.read_text())
        if not isinstance(record, dict):
            raise ValueError(f"invalid briefed batch record: {path}")

        batch_id = record.get("batch_id")
        if type(record.get("schema_version")) is not int or record["schema_version"] != 1 or record.get("repo") != REPO or batch_path(batch_id) != path:
            raise ValueError(f"invalid briefed batch record: {path}")

        keys = record.get("member_keys")
        if not isinstance(keys, list) or not keys or not all(isinstance(key, str) for key in keys) or len(keys) != len(set(keys)):
            raise ValueError(f"invalid briefed batch members: {path}")

        for key in keys:
            kind, _, number = key.partition(":")
            if kind not in ("issue", "pr") or not number.isdigit() or int(number) < 1:
                raise ValueError(f"invalid briefed batch member: {path}")

        brief = record.get("brief")
        if (not isinstance(brief, str) or Path(brief).parent != Path("reports") / REPO or
                not re.fullmatch(rf"\d{{4}}-\d{{2}}-\d{{2}}-batch-{re.escape(batch_id)}-brief\.md", Path(brief).name)):
            raise ValueError(f"invalid briefed batch path: {path}")

        records[batch_id] = record

    return records


def briefed_keys(records=None):
    records = load_records() if records is None else records
    return {key for record in records.values() for key in record["member_keys"]}


def save_briefed(batch_id, members, brief_arg):
    path = batch_path(batch_id)
    requested = Path(brief_arg)
    brief_path = requested.resolve()
    if not requested.is_absolute() and not brief_path.is_file():
        brief_path = (WORK_ROOT / requested).resolve()

    reports_root = REPORTS_DIR.resolve()
    if not brief_path.is_relative_to(reports_root) or not re.fullmatch(rf"\d{{4}}-\d{{2}}-\d{{2}}-batch-{re.escape(batch_id)}-brief\.md", brief_path.name):
        raise ValueError("brief must be a dated batch brief in this repository's reports directory")

    if not brief_path.is_file() or not brief_path.read_text().strip():
        raise ValueError("brief file is missing or empty")

    for row in members:
        if not isinstance(row, dict) or row.get("kind") not in ("issue", "pr") or type(row.get("number")) is not int or row["number"] < 1:
            raise ValueError("invalid batch member")

    keys = sorted({f"{row['kind']}:{row['number']}" for row in members})
    if len(keys) != len(members) or not keys:
        raise ValueError("batch is empty or has duplicate members")

    relative = brief_path.relative_to(WORK_ROOT.resolve()).as_posix()
    with locked():
        previous = load_records().get(batch_id)
        if previous:
            if previous["member_keys"] == keys and previous["brief"] == relative:
                return previous

            raise ValueError("batch was already marked briefed with different members or brief")

        record = dict(schema_version=1, repo=REPO, batch_id=batch_id, member_keys=keys, brief=relative, briefed_at=now_iso())
        with atomic_writer(path) as out:
            json.dump(record, out, ensure_ascii=False, indent=2)
            out.write("\n")

    return record
