"""Versioned, offline evidence contracts. Completeness is declared coverage, never approval."""

import hashlib
import json
import re
from datetime import datetime

from _install import REPO_RE

DEFAULT_MAX_AGE = 86400
VERSION = 1
COMPONENTS = {"summary", "comments", "files", "diff", "reviews", "review_comments", "checks", "closing_issues", "timeline"}
PR_ONLY = {"files", "diff", "reviews", "review_comments", "checks", "closing_issues"}
COLLECTIONS = COMPONENTS - {"summary", "diff"}
FORMATS = {"json": "json", "text": "txt", "diff": "diff"}
STATES = {"complete", "partial", "unavailable", "failed", "not_applicable"}
ITEM_STATES = {"open", "closed", "merged", "unknown", "unavailable", "deleted", "transferred"}
DIGEST = re.compile(r"[0-9a-f]{64}\Z")
HOST = re.compile(r"[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\Z")
SHA = re.compile(r"(?:[0-9a-f]{40}|[0-9a-f]{64})\Z")


def fields(value, required, optional=()):
    if not isinstance(value, dict) or not set(required) <= value.keys() or value.keys() - set(required) - set(optional):
        raise ValueError(f"expected fields {sorted(required)}, optional {sorted(optional)}")


def text(value, name):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{name} must be nonempty text")


def natural(value, name, minimum=0):
    if type(value) is not int or value < minimum:
        raise ValueError(f"{name} must be an integer >= {minimum}")


def timestamp(value):
    text(value, "timestamp")
    try:
        result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise ValueError(f"invalid timestamp: {value!r}") from error

    if result.tzinfo is None:
        raise ValueError("timestamps must include a timezone")

    return result


def version(value, artifact):
    if value.get("artifact") != artifact or type(value.get("schema_version")) is not int or value["schema_version"] != VERSION:
        raise ValueError(f"unsupported {artifact} schema; expected version {VERSION}")


def stable_ids(value):
    if value["database_id"] is not None:
        natural(value["database_id"], "database_id", 1)

    if value["node_id"] is not None:
        text(value["node_id"], "node_id")


def repository(full_name, host="github.com", database_id=None, node_id=None):
    value = dict(host=host, full_name=full_name, database_id=database_id, node_id=node_id)
    validate_repository(value)

    return value


def validate_repository(value):
    fields(value, ("host", "full_name", "database_id", "node_id"))
    if not isinstance(value["host"], str) or not HOST.fullmatch(value["host"]) or ".." in value["host"]:
        raise ValueError("host must be a lowercase hostname, not a URL")

    if not isinstance(value["full_name"], str) or not REPO_RE.fullmatch(value["full_name"]):
        raise ValueError("repository must be owner/repo")

    stable_ids(value)


def same_repository(expected, observed):
    """Fail closed on aliases/transfers and contradictory IDs; never merge namespaces by name alone."""
    validate_repository(expected)
    validate_repository(observed)
    if expected["host"] != observed["host"]:
        raise ValueError("repository host mismatch")

    for key in ("database_id", "node_id"):
        if expected[key] is not None and expected[key] != observed[key]:
            raise ValueError(f"repository {key} mismatch or missing verified ID")

    if expected["full_name"] != observed["full_name"]:
        raise ValueError("repository name/alias changed; reconcile the namespace explicitly before reuse")


def validate_item(value):
    fields(value, ("kind", "number", "database_id", "node_id"))
    if value["kind"] not in ("issue", "pr"):
        raise ValueError("item kind must be issue or pr")

    natural(value["number"], "number", 1)
    stable_ids(value)


def same_item(expected, observed):
    validate_item(expected)
    validate_item(observed)
    if (expected["kind"], expected["number"]) != (observed["kind"], observed["number"]):
        raise ValueError("item moved or changed kind/number; reconcile explicitly")

    for key in ("database_id", "node_id"):
        if expected[key] is not None and expected[key] != observed[key]:
            raise ValueError(f"item {key} mismatch or missing verified ID")


def validate_revision(value):
    fields(value, ("updated_at", "base_sha", "head_sha"))
    if value["updated_at"] is not None:
        timestamp(value["updated_at"])

    for key in ("base_sha", "head_sha"):
        if value[key] is not None and (not isinstance(value[key], str) or not SHA.fullmatch(value[key])):
            raise ValueError(f"{key} must be a full commit SHA or null")


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


def digest(payload):
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def artifact_ref(payload, format="json"):
    if not isinstance(format, str) or format not in FORMATS or not isinstance(payload, str):
        raise ValueError("evidence payload must be UTF-8 text in a supported format")

    if format == "json":
        json.loads(payload, parse_constant=reject_constant)

    return dict(sha256=digest(payload), bytes=len(payload.encode("utf-8")), format=format)


def reject_constant(value):
    raise ValueError(f"non-finite JSON constant: {value}")


def validate_ref(value):
    fields(value, ("sha256", "bytes", "format"))
    if not isinstance(value["sha256"], str) or not DIGEST.fullmatch(value["sha256"]) or not isinstance(value["format"], str) or value["format"] not in FORMATS:
        raise ValueError("invalid evidence object reference")

    natural(value["bytes"], "bytes")


def object_name(ref):
    validate_ref(ref)

    return ref["sha256"] + "." + FORMATS[ref["format"]]


def validate_component(name, value, kind, started, completed):
    fields(value, ("status", "fetched_at", "source", "revision", "expected_count", "received_count", "pagination_complete", "truncated", "error", "object"))
    status = value["status"]
    if not isinstance(status, str) or status not in STATES or type(value["truncated"]) is not bool:
        raise ValueError("invalid component status/truncation")

    validate_revision(value["revision"])
    for key in ("expected_count", "received_count"):
        if value[key] is not None:
            natural(value[key], key)

    if value["pagination_complete"] is not None and type(value["pagination_complete"]) is not bool:
        raise ValueError("pagination_complete must be true, false, or null")

    if status == "not_applicable":
        if kind != "issue" or name not in PR_ONLY or any(value[key] is not None for key in ("source", "fetched_at", "object", "expected_count", "received_count", "pagination_complete", "error")) or value["truncated"]:
            raise ValueError("not_applicable is only for PR-only components on issues, without fetched evidence")

        return

    if kind == "issue" and name in PR_ONLY:
        raise ValueError("PR-only component on an issue must be not_applicable")

    fetched = timestamp(value["fetched_at"])
    # Reused components retain their original observation time, which may precede this acquisition run.
    if fetched > completed:
        raise ValueError("component observation falls after snapshot completion")

    fields(value["source"], ("transport", "resource"))
    if value["source"]["transport"] not in ("rest", "graphql", "gh", "git"):
        raise ValueError("unsupported provenance transport")

    text(value["source"]["resource"], "source resource")
    if value["object"] is not None:
        validate_ref(value["object"])

    if status == "complete":
        if value["object"] is None or value["error"] is not None or value["truncated"]:
            raise ValueError("complete evidence needs an object and no errors/truncation")

        if name in COLLECTIONS and (value["pagination_complete"] is not True or value["received_count"] is None):
            raise ValueError("complete collections need finished pagination and received counts")

        if value["expected_count"] is not None and value["expected_count"] != value["received_count"]:
            raise ValueError("complete component count mismatch")

        required = ("base_sha", "head_sha") if name in ("diff", "files") else ("head_sha",) if name == "checks" else ()
        if any(value["revision"][key] is None for key in required):
            raise ValueError("complete PR code evidence must name its commit revisions")
    else:
        text(value["error"], "incomplete evidence explanation")


def validate_payload(name, component, kind, payload):
    ref = component["object"]
    if artifact_ref(payload, ref["format"]) != ref:
        raise ValueError("evidence object checksum/size mismatch")

    if name == "diff":
        if ref["format"] != "diff":
            raise ValueError("raw diffs must use the diff format")

        return

    if ref["format"] != "json":
        raise ValueError("structured components must use JSON")

    data = json.loads(payload)
    if name in COLLECTIONS:
        if not isinstance(data, list) or component["received_count"] != len(data):
            raise ValueError("collection payload does not match received_count")
    elif name == "summary":
        if not isinstance(data, dict) or not isinstance(data.get("state"), str) or data["state"] not in ITEM_STATES:
            raise ValueError("summary must explicitly distinguish observed and unknown item states")

        if kind == "issue" and data["state"] == "merged":
            raise ValueError("an issue cannot be merged")


def snapshot_id(manifest):
    return digest(canonical({key: value for key, value in manifest.items() if key != "snapshot_id"}))


def seal_snapshot(manifest):
    sealed = dict(manifest, snapshot_id=snapshot_id(manifest))
    validate_snapshot(sealed)

    return sealed


def validate_snapshot(manifest):
    fields(manifest, ("schema_version", "artifact", "snapshot_id", "repository", "started_at", "completed_at", "requested_components", "items"))
    version(manifest, "evidence-snapshot")
    validate_repository(manifest["repository"])
    if manifest["snapshot_id"] != snapshot_id(manifest):
        raise ValueError("snapshot manifest checksum mismatch")

    started, completed = timestamp(manifest["started_at"]), timestamp(manifest["completed_at"])
    if completed < started:
        raise ValueError("acquisition interval is reversed")

    requested = manifest["requested_components"]
    if not isinstance(requested, list) or not requested or any(not isinstance(name, str) or name not in COMPONENTS for name in requested) or len(set(requested)) != len(requested):
        raise ValueError("requested_components must list distinct supported components")

    if not isinstance(manifest["items"], list) or not manifest["items"]:
        raise ValueError("snapshot must declare its selected items")

    seen, stable = set(), set()
    for record in manifest["items"]:
        fields(record, ("identity", "revision", "components"))
        validate_item(record["identity"])
        validate_revision(record["revision"])
        key = (record["identity"]["kind"], record["identity"]["number"])
        if key in seen:
            raise ValueError("duplicate snapshot item")

        seen.add(key)
        for field in ("database_id", "node_id"):
            value = record["identity"][field]
            identifier = (field, key[0] if field == "database_id" else None, value)
            if value is not None:
                if identifier in stable:
                    raise ValueError("stable item ID appears under multiple numbers/kinds")

                stable.add(identifier)

        fields(record["components"], requested)
        for name, component in record["components"].items():
            validate_component(name, component, key[0], started, completed)


def component_problems(name, component, current_revision, now, max_age_seconds):
    """Freshness policy is explicit at read time; a complete historical object may still be stale."""
    validate_revision(current_revision)
    natural(max_age_seconds, "max_age_seconds")
    if component["status"] == "not_applicable":
        return []

    problems = []
    if component["status"] != "complete":
        problems.append(component["status"])

    age = (timestamp(now) - timestamp(component["fetched_at"])).total_seconds()
    if age < 0 or age > max_age_seconds:
        problems.append("observation outside freshness window")

    keys = ("base_sha", "head_sha") if name in ("diff", "files") else ("head_sha",) if name == "checks" else ("updated_at",)
    for key in keys:
        if current_revision[key] is None or component["revision"][key] != current_revision[key]:
            problems.append(f"{key} changed or unknown")

    return problems


def coverage(manifest):
    validate_snapshot(manifest)
    counts = {state: 0 for state in sorted(STATES)}
    for record in manifest["items"]:
        for component in record["components"].values():
            counts[component["status"]] += 1

    return dict(items=len(manifest["items"]), components=counts, complete=not any(counts[state] for state in ("partial", "failed", "unavailable")))
