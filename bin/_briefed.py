"""Brief file names and repository-scoped checkpoints for whole batches screened into maintainer briefs."""

import json
import re
from pathlib import Path

from _storage import atomic_writer, locked
from _triage import BRIEFED_DIR, REPO, REPORTS_DIR, WORK_ROOT, now_iso, parse_key


# Only IDs new_batch_id() can produce, so a batch ID can never reach a path outside its own directory.
BATCH_ID = r"b\d{8}-\d{6}(?:-\d+)?"
BATCH_ID_RE = re.compile(rf"^{BATCH_ID}$")
BRIEF_DATE = r"\d{4}-\d{2}-\d{2}"
ITEM_BRIEF_RE = re.compile(rf"^({BRIEF_DATE})-(issue|pr)-([1-9]\d*)-brief\.md$")
BATCH_BRIEF_RE = re.compile(rf"^({BRIEF_DATE})-batch-({BATCH_ID})-brief\.md$")
# A brief marked read keeps its content under NAME_READ.md and leaves the menus.
READ_SUFFIX = "_READ.md"


def active_name(name):
    return name.removesuffix(READ_SUFFIX) + ".md" if name.endswith(READ_SUFFIX) else name


def read_name(name):
    return name.removesuffix(".md") + READ_SUFFIX


def saved_brief_exists(path):
    return path.exists() or path.with_name(read_name(path.name)).exists()


def visible_batch_briefs():
    """Batch briefs not marked read: the default source set of bin/briefs plan."""
    return sorted(path.name for path in REPORTS_DIR.glob("*-brief.md") if BATCH_BRIEF_RE.fullmatch(path.name) and path.is_file() and not path.is_symlink())


def is_batch_brief(name, batch_id):
    match = BATCH_BRIEF_RE.fullmatch(name)
    return bool(match) and match.group(2) == batch_id


def batch_path(batch_id):
    if not isinstance(batch_id, str) or not BATCH_ID_RE.fullmatch(batch_id):
        raise ValueError(f"{batch_id!r} is not a batch ID")

    return BRIEFED_DIR / f"{batch_id}.json"


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
        if not isinstance(keys, list) or not keys or any(parse_key(key) is None for key in keys) or len(keys) != len(set(keys)):
            raise ValueError(f"invalid briefed batch members: {path}")

        brief = record.get("brief")
        if not isinstance(brief, str) or Path(brief).parent != Path("reports") / REPO or not is_batch_brief(Path(brief).name, batch_id):
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

    # The checkpoint keeps the brief's original name; marking the brief read later renames the file, not the record.
    brief_path = brief_path.with_name(active_name(brief_path.name))
    reports_root = REPORTS_DIR.resolve()
    if not brief_path.is_relative_to(reports_root) or not is_batch_brief(brief_path.name, batch_id):
        raise ValueError("brief must be a dated batch brief in this repository's reports directory")

    saved = brief_path if brief_path.is_file() else brief_path.with_name(read_name(brief_path.name))
    if not saved.is_file() or not saved.read_text().strip():
        raise ValueError("brief file is missing or empty")

    for row in members:
        if not isinstance(row, dict) or row.get("kind") not in ("issue", "pr") or type(row.get("number")) is not int or row["number"] < 1:
            raise ValueError("invalid batch member")

    keys = sorted({f"{row['kind']}:{row['number']}" for row in members})
    if len(keys) != len(members) or not keys:
        raise ValueError("batch is empty or has duplicate members")

    relative = brief_path.relative_to(WORK_ROOT.resolve()).as_posix()
    with locked(BRIEFED_DIR):
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
