"""Install-local guidance and human-controlled permission for agentic verification."""

import json
import re

from _evidence import value_digest
from _storage import atomic_writer, locked
from _triage import CONFIG_DIR, REPO, REPO_RE


SETTINGS_PATH = CONFIG_DIR / "local" / "agentic-verification.json"
MAX_GUIDANCE_CHARS = 16000


def guidance_text(value):
    if not isinstance(value, str) or len(value) > MAX_GUIDANCE_CHARS or "\x00" in value:
        raise ValueError("verification guidance must be text of at most 16000 characters without NUL")

    value.encode("utf-8")
    return value


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("verification settings contain duplicate keys")

        result[key] = value

    return result


def settings():
    if not SETTINGS_PATH.exists() and not SETTINGS_PATH.is_symlink():
        return {"schema_version": 1, "repositories": {}}

    value = json.loads(SETTINGS_PATH.read_text(encoding="utf-8"), object_pairs_hook=unique_object)
    if not isinstance(value, dict) or set(value) != {"schema_version", "repositories"} or type(value["schema_version"]) is not int or value["schema_version"] != 1 or not isinstance(value["repositories"], dict):
        raise ValueError("verification settings are malformed")

    for repo, entry in value["repositories"].items():
        if not REPO_RE.fullmatch(repo) or not isinstance(entry, dict) or set(entry) != {"enabled", "guidance"} or type(entry["enabled"]) is not bool:
            raise ValueError("verification repository setting is malformed")

        guidance_text(entry["guidance"])

    return value


def selected_status(value):
    entry = value["repositories"].get(REPO, {"enabled": False, "guidance": ""})
    result = {"repository": REPO, **entry}
    return {**result, "revision": value_digest(result)}


def status():
    return selected_status(settings())


def update(expected_revision, *, enabled=None, guidance=None):
    if not isinstance(expected_revision, str) or not re.fullmatch(r"[0-9a-f]{64}", expected_revision):
        raise ValueError("verification settings require the current revision")
    if (enabled is None) == (guidance is None) or enabled is not None and type(enabled) is not bool:
        raise ValueError("change exactly one verification setting")
    if guidance is not None:
        guidance_text(guidance)

    with locked(SETTINGS_PATH):
        value = settings()
        current = selected_status(value)
        if current["revision"] != expected_revision:
            raise ValueError("verification settings changed; reload before saving")

        entry = {"enabled": current["enabled"], "guidance": current["guidance"]}
        entry["enabled" if enabled is not None else "guidance"] = enabled if enabled is not None else guidance
        value["repositories"][REPO] = entry
        with atomic_writer(SETTINGS_PATH) as out:
            out.write(json.dumps(value, indent=2, ensure_ascii=False) + "\n")

        return selected_status(value)
