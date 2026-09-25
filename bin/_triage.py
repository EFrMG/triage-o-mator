"""Shared helpers for the triage scripts. Not a CLI entry point.

Importing this module requires an install (see bin/_install.py): every path below is inside the triage-o-mator/ directory of the repository being triaged, never inside the checkout the scripts are symlinked from.
"""

import json
import os
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

sys.path.insert(0, str(Path(__file__).absolute().parent))
# Re-exported: bin/ scripts and bin/_groups.py import now_iso and REPO_RE from here.
from _install import CODE_ROOT, REPO_RE, WORK_ROOT, now_iso, require_install  # noqa: F401
from _storage import atomic_writer

# Every script works on one repository's triage data, which only an install has.
require_install(WORK_ROOT)

# Most people run the tool from the root of the repository being triaged, not from inside the install, so a command this prints has to be pastable from wherever they are standing: nothing inside the install, "triage-o-mator/" from the repository root, "../../triage-o-mator/" from a directory below it.
def _command_prefix():
    try:
        relative = os.path.relpath(WORK_ROOT, Path.cwd())
    except ValueError:  # another drive, on Windows
        return str(WORK_ROOT) + os.sep

    return "" if relative == "." else relative + os.sep


HERE = _command_prefix()

# An install-relative path at the start of a word: never one inside an absolute path (preceded by /), which is already pastable.
_INSTALL_PATH = re.compile(r"(?<![\w./-])(bin|prompts|data|reports|config)/")


def here(text):
    """A command or path with every install-relative reference in it rewritten to where the reader is standing."""
    return _INSTALL_PATH.sub(lambda m: HERE + m.group(0), text) if HERE else text


CONFIG_DIR = WORK_ROOT / "config"
DATA_ROOT = WORK_ROOT / "data"
TAXONOMY_PATH = CONFIG_DIR / "taxonomy.json"

# Canonical field order for ledger records and CSV export. Keep stable so git diffs on ledger.jsonl and exported CSVs stay narrow and readable.
FIELDS = [
    "number",
    "kind",
    "state",
    "title",
    "url",
    "author",
    "created_at",
    "updated_at",
    "labels",
    "comments_count",
    "category",
    "action",
    "confidence",
    "reason",
    "triaged_at",
    "triaged_by",
    "batch_id",
    "agent_notes",
    "reviewed",
    "reviewed_by",
    "reviewed_at",
    "reviewer_notes",
    "first_seen_at",
    "last_synced_at",
]

TRIAGE_DEFAULTS = {
    "category": "",
    "action": "",
    "confidence": "",
    "reason": "",
    "triaged_at": "",
    "triaged_by": "",
    "batch_id": "",
    "agent_notes": "",
    "reviewed": False,
    "reviewed_by": "",
    "reviewed_at": "",
    "reviewer_notes": "",
}


def read_repo():
    path = CONFIG_DIR / "repo"
    if not path.exists():
        sys.exit(f"error: {path} not found. Put an 'owner/repo' string in it.")

    repo = path.read_text().strip()
    if not REPO_RE.match(repo):
        sys.exit(f"error: {path} does not look like 'owner/repo': {repo!r}")

    return repo


REPO = read_repo() if (CONFIG_DIR / "repo").exists() else None

# Everything a repo's triage produces lives in its own folder, so switching config/repo switches data too and two repos' issue numbers can never collide in one ledger.
DATA_DIR = DATA_ROOT.joinpath(*REPO.split("/")) if REPO else DATA_ROOT
RAW_DIR = DATA_DIR / "raw"
BATCHES_DIR = DATA_DIR / "batches"
EXPORTS_DIR = DATA_DIR / "exports"
LEDGER_PATH = DATA_DIR / "ledger.jsonl"
REPORTS_DIR = WORK_ROOT.joinpath("reports", *REPO.split("/")) if REPO else WORK_ROOT / "reports"

def load_taxonomy():
    return json.loads(TAXONOMY_PATH.read_text())


def load_jsonl(path):
    if not path.exists():
        return []

    records = []
    with path.open() as f:
        for line_no, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue

            try:
                records.append(json.loads(line))
            except json.JSONDecodeError as e:
                sys.exit(f"error: {path}:{line_no}: invalid JSON: {e}")

    return records


def save_jsonl(path, records, field_order=None):
    path.parent.mkdir(parents=True, exist_ok=True)
    order = field_order or FIELDS
    with atomic_writer(path) as f:
        for rec in records:
            ordered = {k: rec.get(k, "") for k in order}
            # Preserve any extra fields (e.g. body, diff stats) not in the canonical order, appended after it, so nothing is silently lost.
            for k, v in rec.items():
                if k not in ordered:
                    ordered[k] = v

            f.write(json.dumps(ordered, ensure_ascii=False) + "\n")


def load_ledger():
    return load_jsonl(LEDGER_PATH)


def save_ledger(records):
    records = sorted(records, key=lambda r: (r.get("kind", ""), r.get("number", 0)))
    save_jsonl(LEDGER_PATH, records)


def ledger_key(rec):
    return (rec.get("kind"), rec.get("number"))


def run_gh(args, **kwargs):
    """Run `gh <args>` and return CompletedProcess with captured text output."""
    cmd = ["gh"] + args
    result = subprocess.run(cmd, capture_output=True, text=True, **kwargs)
    if result.returncode != 0:
        sys.exit(
            f"error: `{' '.join(cmd)}` failed (exit {result.returncode}):\n"
            f"{result.stderr.strip()}"
        )

    return result.stdout


def new_batch_id():
    """Reserve a batch ID by exclusively creating its items file, so two batches started in the same second (e.g. from the TUI and a script) never share, and overwrite, each other's files. A clash gets a -2, -3, ... suffix, which still sorts after the plain ID."""
    BATCHES_DIR.mkdir(parents=True, exist_ok=True)
    base = "b" + datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S")
    for n in range(1, 1000):
        batch_id = base if n == 1 else f"{base}-{n}"
        try:
            (BATCHES_DIR / f"{batch_id}.items.jsonl").open("x").close()

            return batch_id
        except FileExistsError:
            continue

    sys.exit(f"error: could not reserve a batch ID starting with {base}")


def enrich_item(rec, include_diff=False):
    """Fetch full body/comments (and, for PRs, diff stats) for one item.

    `rec` only needs `number` and `kind` set. Mutates and returns `rec`.
    Shared by `bin/batch` (bulk, no diff) and `bin/enrich-one` (single item,
    diff optional) so there's exactly one definition of which gh fields we
    pull for an item.
    """
    number = rec["number"]
    if rec["kind"] == "issue":
        out = run_gh(
            [
                "issue",
                "view",
                str(number),
                "--repo",
                REPO,
                "--json",
                "title,body,comments",
            ]
        )
        data = json.loads(out)
    else:
        fields = (
            "title,body,comments,additions,deletions,changedFiles,isDraft,mergeable"
        )
        out = run_gh(["pr", "view", str(number), "--repo", REPO, "--json", fields])
        data = json.loads(out)
        rec["additions"] = data.get("additions")
        rec["deletions"] = data.get("deletions")
        rec["changed_files"] = data.get("changedFiles")
        rec["is_draft"] = data.get("isDraft")
        rec["mergeable"] = data.get("mergeable")
        if include_diff:
            rec["diff_text"] = run_gh(["pr", "diff", str(number), "--repo", REPO])

    comments = data.get("comments", [])
    rec["body"] = data.get("body", "")
    rec["comment_bodies"] = [c.get("body", "") for c in comments]
    rec["comment_authors"] = [(c.get("author") or {}).get("login", "") for c in comments]
    rec["comment_dates"] = [c.get("createdAt") or "" for c in comments]

    return rec
