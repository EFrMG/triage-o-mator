"""Measure a synthetic 5,000-item backlog using real evidence record schemas.

The fixture mixes issues and PRs, discussions, 1% large diffs, and 10% revised
items. It measures allocated blocks, not acquisition speed or a universal bound.
"""

import json
import sys
import tempfile
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "bin"))
from _acquire import PROFILES, descriptor  # noqa: E402
import _acquire  # noqa: E402
from _evidence import PR_ONLY, artifact_ref, canonical, digest, object_name, repository, seal_snapshot  # noqa: E402
from _jobs import sealed  # noqa: E402

OBSERVED = "2026-09-24T00:00:00Z"
COMPLETED = "2026-09-24T00:01:00Z"
PROFILE = PROFILES["backlog"]
_acquire.now = lambda: OBSERVED
COLLECTIONS = {"comments", "files", "reviews", "review_comments", "checks", "closing_issues", "timeline"}
PAGES = {"comments", "files", "reviews", "review_comments", "timeline"}


def main():
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="triage-cache-size-") as temporary:
        root = Path(temporary)
        objects, snapshots, jobs, corpora = (root / name for name in ("objects", "snapshots", "jobs", "corpora"))
        for directory in (objects, snapshots, jobs, corpora):
            directory.mkdir()

        repo = repository("owner/repo", database_id=42, node_id="R_repo")
        index, inventory_records, members = {}, [], []
        inventory_observation = dict(artifact="inventory-observation", schema_version=1, repository=repo, started_at=OBSERVED, completed_at=COMPLETED,
                                     endpoint="repos/owner/repo/issues?state=open&per_page=100", pages=50, pagination_complete=True,
                                     mode="full", since=None, raw_sha256="0" * 64)

        def save_object(value, format="json"):
            payload = canonical(value) + "\n" if format == "json" else value
            ref = artifact_ref(payload, format)
            target = objects / object_name(ref)
            if not target.exists():
                target.write_text(payload)
            return ref

        def save_snapshot(value):
            sealed_value = seal_snapshot(value)
            (snapshots / (sealed_value["snapshot_id"] + ".json")).write_text(canonical(sealed_value) + "\n")
            return sealed_value["snapshot_id"]

        for number in range(1, 5001):
            kind = "pr" if number % 2 == 0 else "issue"
            identity = dict(kind=kind, number=number, database_id=10000 + number, node_id=f"{'PR' if kind == 'pr' else 'I'}_{number}")
            members.append(identity)
            resource = f"repos/owner/repo/{'pulls' if kind == 'pr' else 'issues'}/{number}"
            revision = dict(updated_at=OBSERVED, base_sha="a" * 40 if kind == "pr" else None, head_sha="b" * 40 if kind == "pr" else None)
            inventory_ref = save_object(dict(state="open", source_state="open", kind=kind, number=number, title=f"Change {number}",
                                             body=(f"inventory-{number} context ") * 45, inventory=inventory_observation))
            inventory_component = descriptor("repos/owner/repo/issues?state=open", revision, "complete", None)
            inventory_component["object"] = inventory_ref
            inventory_records.append(dict(identity=identity, revision=revision, components=dict(summary=inventory_component)))

            for generation in range(2 if number % 10 == 0 else 1):
                prefix = f"{kind}-{number}-generation-{generation}"
                bodies = {
                    "summary": dict(state="open", number=number, title=f"Change {number}", body=(prefix + " technical context ") * 45),
                    "comments": [dict(id=number * 100 + n, body=(prefix + " discussion ") * 35) for n in range(3)],
                    "timeline": [dict(id=number * 10 + n, event="commented") for n in range(2)],
                }
                if kind == "pr":
                    bodies.update(files=[dict(filename=f"src/module_{number}_{n}.py", patch=(prefix + " patch ") * 15) for n in range(4)],
                                  diff=(prefix + "\n") + (f"+{number}: modified implementation\n" * ((2_000_000 if number % 100 == 0 else 20_000) // 35 + 1)),
                                  reviews=[dict(id=number * 10 + n, body=(prefix + " review ") * 20) for n in range(2)],
                                  review_comments=[dict(id=number * 1000 + n, body=(prefix + " code review ") * 15) for n in range(2)],
                                  checks=[dict(id=number, state="success")], closing_issues=[])

                complete = {}
                for name in PROFILE:
                    if kind == "issue" and name in PR_ONLY:
                        component = descriptor(resource, revision)
                        component.update(status="not_applicable", fetched_at=None, source=None, expected_count=None, received_count=None, pagination_complete=None, error=None, object=None)
                    else:
                        component = descriptor(resource, revision, "complete", None)
                        component["object"] = save_object(bodies[name], "diff" if name == "diff" else "json")
                        if name in COLLECTIONS:
                            component.update(received_count=len(bodies[name]), pagination_complete=True)

                    complete[name] = component
                    if name in PAGES and (kind == "pr" or name in ("comments", "timeline")):
                        page = dict(schema_version=1, artifact="evidence-page-job", repository=repo, identity=identity, component=name, resource=resource,
                                    revision=revision, expected_count=len(bodies[name]), scope=[["owner/repo", 42, "R_repo"], ["owner/repo", 42, "R_repo"]], state="complete",
                                    pages=[dict(number=1, object=component["object"], more=False, etag=None, fetched_at=OBSERVED)])
                        (jobs / f"{kind}-{number}-{name}.json").write_text(canonical(sealed(page)) + "\n")

                active_names = [name for name in PROFILE if kind == "pr" or name not in PR_ONLY]
                for stage in range(len(active_names) + 1):
                    components = {name: (complete[name] if kind == "issue" and name in PR_ONLY or name in active_names[:stage] else descriptor(resource, revision)) for name in PROFILE}
                    manifest = dict(schema_version=1, artifact="evidence-snapshot", repository=repo, started_at=OBSERVED, completed_at=COMPLETED,
                                    requested_components=PROFILE, items=[dict(identity=identity, revision=revision, components=components)])
                    latest = save_snapshot(manifest)

                index[f"{kind}:{number}"] = dict(identity=identity, snapshot_id=latest, started_at=OBSERVED, completed_at=COMPLETED)

        inventory_manifest = dict(schema_version=1, artifact="evidence-snapshot", repository=repo, started_at=OBSERVED, completed_at=COMPLETED,
                                  requested_components=["summary"], items=inventory_records)
        inventory_id = save_snapshot(inventory_manifest)
        plan = dict(schema_version=1, artifact="evidence-corpus-plan", repository=repo, inventory_snapshot=inventory_id, scope="open-items", profile="backlog", max_age=86400, members=members)
        plan_id = digest(canonical(plan))
        (corpora / (plan_id + ".plan.json")).write_text(canonical(sealed(plan)) + "\n")
        state = dict(schema_version=1, artifact="evidence-corpus-state", plan_id=plan_id, updated_at=COMPLETED, status="finished", last_run=None,
                     items={key: dict(snapshot_id=value["snapshot_id"], outcome="complete", attempts=1, error=None) for key, value in index.items()})
        (corpora / (plan_id + ".state.json")).write_text(canonical(sealed(state)) + "\n")
        current = dict(schema_version=1, artifact="evidence-current-index", repository=repo, history_stamp=None, snapshot_count=len(list(snapshots.iterdir())), items=index)
        (root / "current-index.json").write_text(canonical(sealed(current)) + "\n")

        logical = allocated = count = 0
        for path in root.rglob("*"):
            if path.is_file():
                stat = path.stat()
                logical += stat.st_size
                allocated += stat.st_blocks * 512
                count += 1

    print(json.dumps(dict(items=5000, files=count, logical_bytes=logical, allocated_bytes=allocated, creation_seconds=round(time.monotonic() - started, 2), limit_bytes=5_000_000_000)))


if __name__ == "__main__":
    main()
