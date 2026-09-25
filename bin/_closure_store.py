"""Explicit offline external-closure imports and retained-source inspection, separate from watches and decisions."""

import json

from _acquire import now, summary_identity
from _evidence import canonical, natural, same_item, same_repository, validate_payload
from _external_closures import append_entry, bound_identity, create_history, operation_id, source_pin, validate_history
from _storage import atomic_writer, locked


def path_for(cache, number):
    natural(number, "PR number", 1)
    path = cache.root.parent / "external-closures" / f"pr-{number}.json"
    for candidate in (cache.root.parent, path.parent, path.parent / "local", path):
        if candidate.is_symlink():
            raise ValueError("closure storage must not use symlinks")

    return path


def load(cache, number):
    value = validate_history(json.loads(path_for(cache, number).read_text(encoding="utf-8")))
    same_repository(cache.require_ready(bound=True)["repository"], value["repository"])
    if value["item"]["number"] != number:
        raise ValueError("closure item number mismatch")

    return value


def selected(cache, number, snapshot, event=None, comments=()):
    """Audit one summary and explicitly selected component payloads; other payloads remain unaudited."""
    natural(number, "PR number", 1)
    if event is not None:
        natural(event, "closure event ID", 1)
    for identifier in comments:
        natural(identifier, "comment ID", 1)
    if len(set(comments)) != len(comments):
        raise ValueError("comment IDs must be distinct")

    manifest = cache.load_manifest(snapshot)
    record = next((row for row in manifest["items"] if row["identity"]["kind"] == "pr" and row["identity"]["number"] == number), None)
    if record is None:
        raise ValueError("closure PR is absent from selected snapshot")
    bound_identity(manifest["repository"], record["identity"])
    components = record["components"]
    payloads = {}
    for name in ["summary"] + (["timeline"] if event is not None else []) + (["comments"] if comments else []):
        component = components.get(name)
        if not component or component["object"] is None:
            raise ValueError(f"selected {name} evidence is unavailable")
        resource = f"repos/{manifest['repository']['full_name']}/" + (f"pulls/{number}" if name == "summary" else f"issues/{number}/{name}")
        if component["source"] != dict(transport="rest", resource=resource):
            raise ValueError("selected component source scope mismatch")
        payload = cache.read_object(component["object"])
        validate_payload(name, component, "pr", payload)
        payloads[name] = json.loads(payload)

    raw = payloads["summary"]
    identity, revision = summary_identity(dict(raw, state="closed" if raw["state"] == "merged" else raw["state"]), "pr", number, manifest["repository"])
    if identity != record["identity"] or revision != record["revision"] or revision != components["summary"]["revision"]:
        raise ValueError("closure summary identity/revision mismatch")
    if raw["merged"] != (raw["state"] == "merged"):
        raise ValueError("closure summary merged state mismatch")

    sources = [source_pin("summary", 0, components["summary"]["object"], raw)]
    rows = [raw]
    for name, identifiers in (("timeline", [] if event is None else [event]), ("comments", comments)):
        for identifier in identifiers:
            matches = [(offset, row) for offset, row in enumerate(payloads[name]) if isinstance(row, dict) and type(row.get("id")) is int and row["id"] == identifier]
            if len(matches) != 1:
                raise ValueError(f"selected {name} ID is absent or ambiguous")
            offset, row = matches[0]
            if name == "timeline" and row.get("event") != "closed":
                raise ValueError("selected closure event is not closed")
            prefix = f"https://{manifest['repository']['host']}/{manifest['repository']['full_name']}"
            if name == "comments" and (not isinstance(row.get("body"), str) or row.get("html_url") not in (f"{prefix}/issues/{number}#issuecomment-{identifier}", f"{prefix}/pull/{number}#issuecomment-{identifier}")):
                raise ValueError("selected comment body/scope mismatch")
            api = "https://api.github.com" if manifest["repository"]["host"] == "github.com" else f"https://{manifest['repository']['host']}/api/v3"
            expected_url = f"{api}/repos/{manifest['repository']['full_name']}/issues/{'events' if name == 'timeline' else 'comments'}/{identifier}"
            if "url" in row and row["url"] != expected_url:
                raise ValueError("selected source URL scope mismatch")
            sources.append(source_pin(name, offset, components[name]["object"], row))
            rows.append(row)

    gaps = []
    for name in ("summary", "comments", "timeline"):
        component = components.get(name)
        if not component or component["status"] != "complete":
            gaps.append(dict(component=name, reason="Not captured" if not component else component["error"] or "Incomplete component"))
        if component and component["revision"] != revision:
            gaps.append(dict(component=name, reason="Component revision differs from summary"))
    if event is None:
        gaps.append(dict(component="operation", reason="No explicit retained closed event selected"))

    observation = dict(snapshot_id=snapshot, observed_at=manifest["completed_at"], revision=revision, state=raw["state"], closed_at=raw.get("closed_at"), sources=sources,
                       closure_source=None if event is None else 1, operation_id=None if event is None else operation_id(manifest["repository"], identity, event), gaps=gaps)

    return manifest["repository"], identity, observation, rows


def audit(cache, history, observation):
    sources = observation["sources"]
    closure = observation["closure_source"]
    repository, item, rebuilt, rows = selected(cache, history["item"]["number"], observation["snapshot_id"],
                                               None if closure is None else sources[closure]["source_id"],
                                               [source["source_id"] for source in sources if source["component"] == "comments"])
    if repository != history["repository"] or item != history["item"] or rebuilt != observation:
        raise ValueError("retained closure observation does not match selected evidence")

    return dict(status="verified-selected-sources", scope="manifest and selected component payloads; unrelated payloads unaudited; no freshness or provenance authentication", sources=rows)


def import_closure(cache, number, snapshot, by, claim, *, event=None, comments=(), kind="import", predecessor=None, reason=None, checkpoint=None, at=None):
    repository, item, observation, rows = selected(cache, number, snapshot, event, comments)
    path = path_for(cache, number)
    with locked(path):
        existing = path.exists()
        history = load(cache, number) if existing else create_history(repository, item)
        if history["repository"] != repository or history["item"] != item:
            raise ValueError("closure history repository/item identity mismatch")
        expected = checkpoint if existing or checkpoint is not None else history["checksum"]
        result = append_entry(history, observation, claim, by=by, at=at or now(), checkpoint=expected, kind=kind, predecessor=predecessor, reason=reason)
        changed = result != history
        if changed:
            with atomic_writer(path) as out:
                out.write(canonical(result) + "\n")

    return dict(status="imported" if changed else "unchanged", history=result, audit=dict(status="verified-selected-sources", components=list(dict.fromkeys(source["component"] for source in observation["sources"])), scope="manifest and selected component payloads; unrelated payloads unaudited; no freshness or provenance authentication"))


def watch_link(cache, history):
    from _watch import load as load_watch, path_for as watch_path

    path = watch_path(cache, history["item"]["number"])
    if not path.exists():
        return dict(status="not-enrolled")
    try:
        watch = load_watch(cache, history["item"]["number"])
        same_repository(history["repository"], watch["repository"])
        same_item(history["item"], watch["item"])
        if history["repository"] != watch["repository"] or history["item"] != watch["item"]:
            raise ValueError("watch binding differs")
        if load_watch(cache, history["item"]["number"])["checksum"] != watch["checksum"]:
            return dict(status="stale", checksum=watch["checksum"], reason="Watch changed during linkage read; retry")
        return dict(status="linked", checksum=watch["checksum"], case=watch.get("case"), observations=watch["observations"],
                    scope="identity linkage only; watch payloads and freshness unaudited")
    except (OSError, ValueError, KeyError, TypeError) as error:
        return dict(status="unavailable", reason=str(error))


def inspect(cache, number):
    history = load(cache, number)
    observations = []
    for entry in history["entries"]:
        try:
            result = audit(cache, history, entry["observation"])
        except FileNotFoundError as error:
            result = dict(status="unavailable", reason=str(error))
        observations.append(dict(entry_id=entry["id"], **result))
    if load(cache, number)["checksum"] != history["checksum"]:
        raise ValueError("closure history changed during inspection; retry")

    return dict(history=history, observations=observations, watch=watch_link(cache, history))
