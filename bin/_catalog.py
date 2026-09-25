"""Offline catalog pages. Metadata candidates are not audited inventories or permission to acquire."""

import stat

from _chunks import page, window
from _corpus import load_plan
from _evidence import DIGEST, canonical, digest


def names(cache, kind):
    directory, suffix = ("snapshots", ".json") if kind == "inventories" else ("corpora", ".plan.json")
    root = cache.path(directory)
    if not root.exists():
        return []

    identifiers = []
    for path in root.iterdir():
        if path.name.endswith(suffix):
            identifier = path.name[:-len(suffix)]
            if not DIGEST.fullmatch(identifier):
                raise ValueError("catalog has an unrecognized artifact filename; inspect cache storage")

            identifiers.append(identifier)

    return sorted(identifiers)


def entry(cache, kind, identifier):
    directory, suffix = ("snapshots", ".json") if kind == "inventories" else ("corpora", ".plan.json")
    try:
        target = cache.path(directory, identifier + suffix)
        if not stat.S_ISREG(target.lstat().st_mode):
            raise ValueError("catalog artifact is not a regular file")

        if kind == "corpora":
            plan = load_plan(cache, identifier, metadata_only=True)
            return dict(id=identifier, selectable=True, classification="corpus-plan", inventory_snapshot=plan["inventory_snapshot"],
                        scope=plan["scope"], profile=plan["profile"], max_age=plan["max_age"], members=len(plan["members"]))

        manifest = cache.load_manifest(identifier)
        endpoint = f"repos/{manifest['repository']['full_name']}/issues?state=open&per_page=100"
        # Inventory provenance is in the payload, which discovery does not open. Full creation revalidates every member's provenance and source state.
        if manifest["requested_components"] != ["summary"]:
            return None

        for row in manifest["items"]:
            component = row["components"].get("summary")
            if (not component or component["status"] != "partial" or not component["object"] or
                    component["source"] != dict(transport="rest", resource=endpoint)):
                return None

        issues = sum(row["identity"]["kind"] == "issue" for row in manifest["items"])
        return dict(id=identifier, selectable=True, classification="inventory-candidate", started_at=manifest["started_at"],
                    completed_at=manifest["completed_at"], members=len(manifest["items"]), issues=issues, prs=len(manifest["items"]) - issues)
    except (ValueError, OSError, TypeError, KeyError):
        # Do not hide corrupt/future/foreign artifacts as if they did not exist, and do not echo unbounded source or exception text in a bounded page.
        return dict(id=identifier, selectable=False, classification="unavailable")


def discover(cache, kind, offset=0, limit=20, checkpoint=None):
    if kind not in ("inventories", "corpora"):
        raise ValueError("unsupported catalog kind")

    window(offset, limit, 100)
    if offset and checkpoint is None:
        raise ValueError("catalog continuation requires --checkpoint from the first page")

    metadata = cache.metadata()
    repository = metadata["repository"] if metadata else cache.identity
    identifiers = names(cache, kind) if metadata else []
    token = digest(canonical(dict(repository=repository, kind=kind, identifiers=identifiers)))
    if checkpoint is not None and checkpoint != token:
        raise ValueError("catalog membership changed or mismatched; restart discovery at offset zero")

    selected = identifiers[offset:offset + limit]
    pagination = page(len(identifiers), offset, len(selected))
    rows = [entry(cache, kind, identifier) for identifier in selected]
    if metadata != cache.metadata() or (metadata and identifiers != names(cache, kind)):
        raise ValueError("catalog changed during discovery; restart at offset zero")

    return dict(kind=kind, repository=repository, checkpoint=token, pagination=pagination,
                items=[row for row in rows if row is not None], excluded=len([row for row in rows if row is None]),
                cache_status="present" if metadata else "not-initialized",
                continuation=dict(offset=pagination["next_offset"], limit=limit, checkpoint=token) if pagination["next_offset"] is not None else None,
                ordering="lexicographic artifact ID, not observation date",
                verification="metadata candidates only; no payload, full inventory provenance, progress or freshness audit",
                pagination_basis="artifact files examined, not eligible inventory count; empty pages may have continuation",
                requests=0)
