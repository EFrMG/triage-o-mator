"""Repository-scoped requirement for passing observed CI in ready PR reviews."""

import json
import re

from _evidence import value_digest
from _storage import atomic_writer, locked
from _triage import CONFIG_DIR, REPO, REPO_RE


POLICY_PATH = CONFIG_DIR / "ci-readiness.json"


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("CI readiness policy contains duplicate keys")

        result[key] = value

    return result


def policy():
    if not POLICY_PATH.exists() and not POLICY_PATH.is_symlink():
        return dict(schema_version=1, repositories={})

    value = json.loads(POLICY_PATH.read_text(encoding="utf-8"), object_pairs_hook=unique_object)
    if not isinstance(value, dict) or set(value) != {"schema_version", "repositories"} or type(value["schema_version"]) is not int or value["schema_version"] != 1 or not isinstance(value["repositories"], dict):
        raise ValueError("CI readiness policy is malformed")

    for repo, setting in value["repositories"].items():
        if not REPO_RE.fullmatch(repo) or not isinstance(setting, dict) or set(setting) != {"enabled"} or type(setting["enabled"]) is not bool:
            raise ValueError("CI readiness repository setting is malformed")

    return value


def status():
    enabled = policy()["repositories"].get(REPO, {}).get("enabled", False)
    selected = dict(repository=REPO, enabled=enabled)

    return dict(selected, revision=value_digest(selected))


def set_enabled(enabled, expected_revision):
    if type(enabled) is not bool or not isinstance(expected_revision, str) or re.fullmatch(r"[0-9a-f]{64}", expected_revision) is None:
        raise ValueError("select a boolean CI requirement and a valid expected revision")

    with locked(POLICY_PATH):
        if status()["revision"] != expected_revision:
            raise ValueError("CI readiness policy changed; reload Settings")

        saved = policy()
        saved["repositories"][REPO] = dict(enabled=enabled)
        with atomic_writer(POLICY_PATH) as out:
            out.write(json.dumps(saved, indent=2, ensure_ascii=False) + "\n")

        return status()
