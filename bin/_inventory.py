"""Versioned list observations: useful source text, never verified PR detail or closure provenance."""

import json

from _acquire import GitHubReader, attach, descriptor, now
from _evidence import digest, fields, natural, repository, same_repository, seal_snapshot, timestamp, validate_item, version
from _jobs import Cooldown
from _triage import RAW_DIR
from _storage import locked


def capture(cache, endpoint, budget):
    reader = GitHubReader(cache.identity["host"], budget, Cooldown(cache))
    started = now()
    raw_repo, _ = reader.get(f"repos/{cache.identity['full_name']}")
    observed = repository(raw_repo["full_name"], host=cache.identity["host"], database_id=raw_repo["id"], node_id=raw_repo["node_id"])
    same_repository(cache.identity, observed)
    cache.bind_repository(observed)
    rows, seen = [], set()
    page = 1
    while True:
        values, headers = reader.get(endpoint + f"&page={page}")
        if not isinstance(values, list) or len(values) > 100:
            raise ValueError("inventory page must be an array of at most 100 items")

        for value in values:
            kind = "pr" if "pull_request" in value else "issue"
            number = value["number"]
            natural(number, "inventory number", 1)
            if number in seen:
                raise ValueError("repeated inventory number; refetch instead of publishing mixed pages")

            seen.add(number)
            rows.append(dict(number=number, kind=kind, title=value["title"], body=value["body"], url=value["html_url"], author=(value.get("user") or {}).get("login"), created_at=value["created_at"], updated_at=value["updated_at"], state=value["state"], state_reason=value.get("state_reason"), labels=[label["name"] for label in value["labels"]], comments_count=value["comments"], inventory_ids=dict(database_id=value["id"], node_id=value["node_id"])))

        if 'rel="next"' not in headers.get("link", ""):
            break

        page += 1

    return rows, dict(artifact="inventory-observation", schema_version=1, repository=observed, started_at=started, completed_at=now(), endpoint=endpoint, pages=page, pagination_complete=True)


def prepare(cache, meta, payload):
    observation = meta.get("inventory")
    if not isinstance(observation, dict):
        raise ValueError("legacy inventory has no verified provenance; run bin/fetch --cache-inventory")

    fields(observation, ("artifact", "schema_version", "repository", "started_at", "completed_at", "endpoint", "pages", "pagination_complete"))
    version(observation, "inventory-observation")
    same_repository(cache.identity, observation["repository"])
    if observation["repository"]["database_id"] is None or observation["repository"]["node_id"] is None:
        raise ValueError("inventory lacks verified repository IDs")

    if meta.get("repo") != cache.identity["full_name"] or digest(payload) != meta.get("raw_sha256"):
        raise ValueError("inventory repository/checksum mismatch; refetch before importing")

    if timestamp(observation["started_at"]) > timestamp(observation["completed_at"]):
        raise ValueError("inventory interval is reversed")

    natural(observation["pages"], "inventory pages", 1)
    if observation["pagination_complete"] is not True:
        raise ValueError("inventory pagination is incomplete")

    mode, since = meta.get("mode"), meta.get("since")
    endpoint = f"repos/{cache.identity['full_name']}/issues?state="
    if mode == "full" and since is None:
        endpoint += "open&per_page=100"
    elif mode == "incremental":
        timestamp(since)
        endpoint += f"all&since={since}&per_page=100"
    else:
        raise ValueError("invalid inventory scope")

    if observation["endpoint"] != endpoint:
        raise ValueError("inventory endpoint does not match its recorded scope")

    rows = [json.loads(line) for line in payload.splitlines() if line.strip()]
    natural(meta.get("issues"), "inventory issue count")
    natural(meta.get("prs"), "inventory PR count")
    if len(rows) > observation["pages"] * 100:
        raise ValueError("inventory count exceeds declared page capacity")

    items, payloads, seen, seen_ids = [], {}, set(), set()
    for row in rows:
        fields(row, ("number", "kind", "title", "body", "url", "author", "created_at", "updated_at", "state", "state_reason", "labels", "comments_count", "inventory_ids"))
        ids = row["inventory_ids"]
        validate_item(dict(kind=row["kind"], number=row["number"], **ids))
        if ids["database_id"] is None or ids["node_id"] is None or row["number"] in seen:
            raise ValueError("inventory has missing IDs or repeated numbers")

        seen.add(row["number"])
        for name, value in ids.items():
            if (name, value) in seen_ids:
                raise ValueError("inventory source ID appears under multiple numbers")

            seen_ids.add((name, value))

        kind, number = row["kind"], row["number"]
        expected_url = f"https://{cache.identity['host']}/{cache.identity['full_name']}/{'pull' if kind == 'pr' else 'issues'}/{number}"
        if row["url"] != expected_url or row["state"] not in ("open", "closed") or (mode == "full" and row["state"] != "open"):
            raise ValueError("inventory URL/kind/state does not match scope")

        if not isinstance(row["title"], str) or (row["body"] is not None and not isinstance(row["body"], str)):
            raise ValueError("inventory title/body is invalid")

        timestamp(row["created_at"])
        timestamp(row["updated_at"])
        if not isinstance(row["labels"], list) or any(not isinstance(label, str) for label in row["labels"]):
            raise ValueError("inventory labels must be a list of names")

        if row["author"] is not None and not isinstance(row["author"], str):
            raise ValueError("inventory author must be a login or null")

        natural(row["comments_count"], "inventory comment count")
        revision = dict(updated_at=row["updated_at"], base_sha=None, head_sha=None)
        # The issues endpoint's database ID for a PR is not its pulls endpoint database ID. Retain source IDs without binding them to PR detail identity.
        identity = dict(kind=kind, number=number, **(ids if kind == "issue" else dict(database_id=None, node_id=None)))
        data = dict(row, html_url=row["url"], source_state=row["state"], inventory=dict(observation, mode=mode, since=since, raw_sha256=meta["raw_sha256"]))
        if kind == "pr" and row["state"] == "closed":
            data["state"] = "unknown"

        gap = "list observation only; detail and discussion not verified"
        if kind == "pr":
            gap += "; PR revisions and closed PR merge state unknown"

        component = descriptor(endpoint, revision, status="partial", error=gap)
        component["fetched_at"] = observation["started_at"]
        attach(component, data, payloads)
        items.append(dict(identity=identity, revision=revision, components=dict(summary=component)))

    if meta.get("issues") != sum(row["kind"] == "issue" for row in rows) or meta.get("prs") != sum(row["kind"] == "pr" for row in rows):
        raise ValueError("inventory counts do not match payload")

    if not items:
        return None, {}

    manifest = seal_snapshot(dict(schema_version=1, artifact="evidence-snapshot", repository=observation["repository"], started_at=observation["started_at"], completed_at=observation["completed_at"], requested_components=["summary"], items=items))

    return manifest, payloads


def publish(cache, meta, payload):
    manifest, payloads = prepare(cache, meta, payload)
    cache.initialize()
    cache.bind_repository(meta["inventory"]["repository"])
    snapshot_id = cache.publish(manifest, payloads) if manifest else None

    return dict(snapshot_id=snapshot_id, items=len(manifest["items"]) if manifest else 0, requests=0, scope=meta["mode"], coverage="list summaries only; no absence or closure inference")


def import_inventory(cache):
    # Same ordering as fetch: inventory metadata, then cache acquisition, then publication metadata.
    with locked(RAW_DIR / "fetch_meta.json"):
        meta = json.loads((RAW_DIR / "fetch_meta.json").read_text(encoding="utf-8"))
        payload = (RAW_DIR / "issues_and_prs.jsonl").read_bytes().decode("utf-8")
        prepare(cache, meta, payload)
        cache.initialize()
        with locked(cache.path("acquisition")):
            return publish(cache, meta, payload)
