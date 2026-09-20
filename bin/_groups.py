"""Durable, repository-scoped review groups; no GitHub or ledger mutations."""

import fcntl
import json
import os
import re
import tempfile
import uuid
from contextlib import contextmanager

from _triage import DATA_DIR, REPO, now_iso

GROUPS_DIR = DATA_DIR / "groups"
STATUSES = ("draft", "ready", "archived")


def group_path(group_id):
    if not re.fullmatch(r"[a-zA-Z0-9_-]{1,80}", group_id):
        raise ValueError("invalid group ID")

    return GROUPS_DIR / f"{group_id}.json"


def validate(group):
    group_path(group["id"])
    if group.get("schema_version") != 1 or group.get("repo") != REPO:
        raise ValueError("group schema or repository does not match")

    if not isinstance(group.get("title"), str) or not group["title"].strip():
        raise ValueError("a group needs a title")

    for field in ("description", "assignee", "created_by", "updated_by"):
        if not isinstance(group.get(field), str):
            raise ValueError(f"invalid {field}")

    if group.get("status") not in STATUSES:
        raise ValueError("invalid group status")

    if not isinstance(group.get("revision"), int) or group["revision"] < 1:
        raise ValueError("invalid revision")

    seen = set()
    for member in group["members"]:
        key = (member["kind"], member["number"])
        if key[0] not in ("issue", "pr") or type(key[1]) is not int or key[1] < 1:
            raise ValueError("invalid member reference")

        if key in seen or not isinstance(member.get("notes"), str):
            raise ValueError("duplicate member or invalid notes")

        seen.add(key)


def load_group(group_id):
    group = json.loads(group_path(group_id).read_text())
    validate(group)
    if group["id"] != group_id:
        raise ValueError("group ID does not match filename")

    return group


def list_groups():
    groups = []
    for path in sorted(GROUPS_DIR.glob("*.json")):
        group = json.loads(path.read_text())
        if group.get("repo") == REPO:
            validate(group)
            if group["id"] != path.stem:
                raise ValueError("group ID does not match filename")

            groups.append(group)

    return sorted(groups, key=lambda g: (g["status"] == "archived", g["title"].casefold(), g["id"]))


@contextmanager
def locked():
    GROUPS_DIR.mkdir(parents=True, exist_ok=True)
    with (GROUPS_DIR / ".lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        yield


def save_group(group):
    validate(group)
    path = group_path(group["id"])
    fd, name = tempfile.mkstemp(prefix=".group-", dir=GROUPS_DIR)
    try:
        with os.fdopen(fd, "w") as out:
            json.dump(group, out, ensure_ascii=False, indent=2)
            out.write("\n")
            out.flush()
            os.fsync(out.fileno())

        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def create_group(title, description, assignee, by):
    now = now_iso()

    return dict(schema_version=1, id=uuid.uuid4().hex, repo=REPO,
                title=title, description=description, assignee=assignee,
                status="draft", revision=1, created_by=by, created_at=now,
                updated_by=by, updated_at=now, members=[])
