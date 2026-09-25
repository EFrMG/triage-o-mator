"""PR comparison sources: conservative diff coverage, revision-scoped checks, and structured closing relationships."""

import re

from _acquire import ReadFailure, attach, descriptor, now
from _evidence import artifact_ref, natural, object_name, repository, same_repository, text, validate_item


def incomplete(component, error, has_data):
    component.update(status="partial" if has_data else "unavailable" if isinstance(error, ReadFailure) and error.unavailable else "failed", error=str(error))


def verify_diff(diff, files, summary):
    """Accept a deliberately narrow ordinary-text format. Unsupported changes remain readable but never silently complete."""
    natural(summary.get("changed_files"), "changed_files")
    if len(files) != summary["changed_files"]:
        raise ValueError("diff coverage needs the full changed-file list")

    blocks = {}
    current = None
    for line in diff.split("\n"):
        if line.startswith("diff --git "):
            if line in blocks:
                raise ValueError("duplicate diff file header")

            blocks[line] = current = []
        elif current is not None:
            current.append(line)
        elif line:
            raise ValueError("unsupported diff preamble")

    if len(blocks) != len(files):
        raise ValueError("diff file coverage differs from the changed-file list")

    total_added = total_deleted = 0
    for file in files:
        name, old = file["filename"], file.get("previous_filename", file["filename"])
        if any(not isinstance(path, str) or not re.fullmatch(r"[A-Za-z0-9_./-]+", path) for path in (name, old)):
            raise ValueError("quoted or unusual diff paths need additional verification")

        lines = blocks.get(f"diff --git a/{old} b/{name}")
        if lines is None:
            raise ValueError("diff path/rename differs from the changed-file list")

        if any("160000" in line for line in lines[:4]) or any(line.startswith(("Binary files ", "GIT binary patch", "+Subproject commit ", "-Subproject commit ")) for line in lines):
            raise ValueError("binary or submodule changes need additional code evidence")

        start = next((i for i, line in enumerate(lines) if line.startswith("@@")), len(lines))
        if start == len(lines):
            raise ValueError("metadata-only or omitted hunks need additional verification")

        old_marker = "--- /dev/null" if file.get("status") == "added" else f"--- a/{old}"
        new_marker = "+++ /dev/null" if file.get("status") == "removed" else f"+++ b/{name}"
        if old_marker not in lines[:start] or new_marker not in lines[:start]:
            raise ValueError("missing or inconsistent diff content paths")

        if any(not line.startswith(("index ", "--- ", "+++ ", "old mode ", "new mode ", "new file mode ", "deleted file mode ", "similarity index ", "dissimilarity index ", "rename from ", "rename to ", "copy from ", "copy to ")) for line in lines[:start]):
            raise ValueError("unsupported diff extended header")

        hunks = lines[start:]
        if hunks and hunks[-1] == "":
            hunks = hunks[:-1]

        patch = file.get("patch")
        if not isinstance(patch, str) or "\n".join(hunks) != patch.rstrip("\n"):
            raise ValueError("raw diff hunks differ from supplied file patches or patches are omitted")

        added = deleted = old_left = new_left = 0
        for line in hunks:
            if line.startswith("@@"):
                if old_left or new_left:
                    raise ValueError("truncated diff hunk")

                match = re.fullmatch(r"@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@(?: .*)?", line)
                if not match:
                    raise ValueError("unsupported diff hunk header")

                old_left, new_left = (int(value) if value is not None else 1 for value in match.groups())
            elif line == "\\ No newline at end of file":
                continue
            elif line.startswith("+"):
                new_left -= 1
                added += 1
            elif line.startswith("-"):
                old_left -= 1
                deleted += 1
            elif line.startswith(" "):
                old_left -= 1
                new_left -= 1
            else:
                raise ValueError("unsupported or truncated diff hunk content")

            if old_left < 0 or new_left < 0:
                raise ValueError("diff hunk exceeds declared line counts")

        if old_left or new_left:
            raise ValueError("truncated diff hunk")

        for key in ("additions", "deletions"):
            natural(file.get(key), f"file {key}")

        if (added, deleted) != (file["additions"], file["deletions"]):
            raise ValueError("diff line counts differ from file metadata")

        total_added += added
        total_deleted += deleted

    for key in ("additions", "deletions"):
        natural(summary.get(key), f"PR {key}")

    if (total_added, total_deleted) != (summary["additions"], summary["deletions"]):
        raise ValueError("diff line counts differ from PR metadata")


def collect_diff(reader, resource, revision, files, file_data, summary, payloads):
    component = descriptor(resource + " (Accept: application/vnd.github.diff)", revision)
    try:
        diff, _ = reader.diff(resource)
        ref = artifact_ref(diff, "diff")
        payloads[object_name(ref)] = diff
        component["object"] = ref
        if files["status"] != "complete" or any(files["revision"][key] != revision[key] for key in ("base_sha", "head_sha")):
            raise ValueError("diff coverage needs complete files at the observed revision")

        verify_diff(diff, file_data, summary)
        component.update(status="complete", error=None)
    except (ReadFailure, ValueError) as error:
        incomplete(component, error, component["object"] is not None)

    component["fetched_at"] = now()
    return component


def pages(reader, resource, collection=None):
    """Yield verified REST pages. Endpoint counts and page completion are checked even for nested check collections."""
    page, received, expected = 1, 0, None
    seen = set()
    while True:
        separator = "&" if "?" in resource else "?"
        data, headers = reader.get(f"{resource}{separator}per_page=100&page={page}")
        if collection:
            if not isinstance(data, dict):
                raise ReadFailure("check collection response is not an object")

            count = data.get("total_count")
            if type(count) is not int or count < 0 or (expected is not None and expected != count):
                raise ReadFailure("check collection count missing or changed during pagination")

            expected = count
            data = data.get(collection)

        if not isinstance(data, list) or len(data) > 100:
            raise ReadFailure("invalid check/status page")

        for row in data:
            identifier = row.get("id") if isinstance(row, dict) else None
            if type(identifier) is not int or identifier <= 0 or identifier in seen:
                raise ReadFailure("missing or repeated check/status identity")

            seen.add(identifier)

        received += len(data)
        yield data
        if not re.search(r'rel="next"', headers.get("link", "")):
            if expected is not None and received != expected:
                raise ReadFailure("check collection count does not match received pages")

            return

        page += 1


def collect_checks(reader, repo, summary, revision, payloads):
    sha = revision["head_sha"]
    component = descriptor(f"head {sha}: base/head repositories, check-suites -> check-runs(filter=all), commit statuses", revision)
    component.update(received_count=0, pagination_complete=False)
    values, errors = [], []
    repositories = [repo]
    try:
        raw_head = (summary.get("head") or {}).get("repo")
        if not raw_head:
            raise ValueError("head repository unavailable; fork check coverage is unknown")

        head = repository(raw_head.get("full_name"), repo["host"], raw_head.get("id"), raw_head.get("node_id"))
        if head["database_id"] is None or head["node_id"] is None:
            raise ValueError("head repository lacks stable identity")

        if head["full_name"] == repo["full_name"]:
            same_repository(repo, head)
        else:
            raw, _ = reader.get(f"repos/{head['full_name']}")
            same_repository(head, repository(raw.get("full_name"), repo["host"], raw.get("id"), raw.get("node_id")))
            repositories.append(head)
    except (ValueError, ReadFailure) as error:
        errors.append(str(error))

    for source_repo in repositories:
        prefix = f"repos/{source_repo['full_name']}"

        def retain(kind, rows, resource):
            for row in rows:
                if kind != "status" and row.get("head_sha") != sha:
                    raise ReadFailure("check evidence returned a different head SHA")

                values.append(dict(kind=kind, repository=source_repo, head_sha=sha, fetched_at=now(), resource=resource, data=row))

        try:
            suites = f"{prefix}/commits/{sha}/check-suites"
            for rows in pages(reader, suites, "check_suites"):
                retain("check_suite", rows, suites)
                for suite in rows:
                    runs = f"{prefix}/check-suites/{suite['id']}/check-runs?filter=all"
                    for run_rows in pages(reader, runs, "check_runs"):
                        if any((row.get("check_suite") or {}).get("id") != suite["id"] for row in run_rows):
                            raise ReadFailure("check run belongs to a different suite")

                        retain("check_run", run_rows, runs)
        except ReadFailure as error:
            errors.append(str(error))

        try:
            statuses = f"{prefix}/commits/{sha}/statuses"
            for rows in pages(reader, statuses):
                retain("status", rows, statuses)
        except ReadFailure as error:
            errors.append(str(error))

    component.update(fetched_at=now(), received_count=len(values), pagination_complete=not errors, status="partial" if errors else "complete", error="; ".join(errors) if errors else None)
    attach(component, values, payloads)
    return component


def closing_page_rows(repo, connection, seen):
    count = connection["totalCount"]
    natural(count, "closing issue count")
    nodes = connection["nodes"]
    if not isinstance(nodes, list) or len(nodes) > 100:
        raise ValueError("invalid closing issue page")

    values = []
    for issue in nodes:
        linked_repo = repository(issue["repository"]["nameWithOwner"], repo["host"], node_id=issue["repository"]["id"])
        text(linked_repo["node_id"], "linked repository ID")
        if linked_repo["full_name"] == repo["full_name"] and linked_repo["node_id"] != repo["node_id"]:
            raise ValueError("linked issue contradicts the bound repository identity")

        linked = dict(kind="issue", number=issue["number"], database_id=None, node_id=issue["id"])
        validate_item(linked)
        text(linked["node_id"], "linked issue ID")
        if linked["node_id"] in seen or issue["url"] != f"https://{repo['host']}/{linked_repo['full_name']}/issues/{linked['number']}" or issue["state"] not in ("OPEN", "CLOSED"):
            raise ValueError("repeated or inconsistent linked issue identity/state")

        seen.add(linked["node_id"])
        values.append(dict(repository=linked_repo, identity=linked, url=issue["url"], state=issue["state"].lower(), updated_at=issue["updatedAt"]))

    info = connection["pageInfo"]
    if type(info["hasNextPage"]) is not bool:
        raise ValueError("missing GraphQL pagination completion")

    return count, values, info


def collect_closing(reader, repo, identity, revision, payloads):
    component = descriptor(f"ClosingIssues node {identity['node_id']}: closingIssuesReferences(first:100)", revision)
    component["source"]["transport"] = "graphql"
    component.update(received_count=0, pagination_complete=False)
    values, seen, cursors = [], set(), set()
    cursor = None
    try:
        while True:
            node = reader.closing_page(identity["node_id"], cursor)
            if node is None:
                raise ReadFailure("PR or closing relationships unavailable through GraphQL", unavailable=True)

            source_repo = node["repository"]
            if source_repo["id"] != repo["node_id"] or source_repo["nameWithOwner"] != repo["full_name"] or node["id"] != identity["node_id"] or node["number"] != identity["number"] or node["url"] != f"https://{repo['host']}/{repo['full_name']}/pull/{identity['number']}":
                raise ValueError("GraphQL repository/PR identity differs from the verified REST identity")

            if (node["baseRefOid"], node["headRefOid"], node["updatedAt"]) != (revision["base_sha"], revision["head_sha"], revision["updated_at"]):
                raise ValueError("PR revision changed during closing-issue acquisition")

            connection = node["closingIssuesReferences"]
            if connection is None:
                raise ReadFailure("closing relationships unavailable", unavailable=True)

            count, rows, info = closing_page_rows(repo, connection, seen)
            if component["expected_count"] is not None and component["expected_count"] != count:
                raise ValueError("closing issue count changed during pagination")

            component["expected_count"] = count
            values.extend(rows)

            if not info["hasNextPage"]:
                component["pagination_complete"] = True
                break

            cursor = info["endCursor"]
            text(cursor, "GraphQL continuation cursor")
            if cursor in cursors:
                raise ValueError("GraphQL continuation cursor repeated")

            cursors.add(cursor)

        if len(values) != component["expected_count"]:
            raise ValueError("closing issue count differs from received pages")

        component.update(status="complete", error=None)
    except (ReadFailure, ValueError, KeyError, TypeError) as error:
        incomplete(component, error, bool(values))

    component.update(fetched_at=now(), received_count=len(values))
    attach(component, values, payloads)
    return component
