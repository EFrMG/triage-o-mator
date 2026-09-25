"""Fixed, bounded GraphQL pages and one Git transfer per corpus batch."""

import os
import re
import subprocess

from _acquire import ReadFailure, attach, descriptor, now, summary_identity
from _evidence import artifact_ref, natural, object_name, repository, same_item, same_repository, seal_snapshot
from _pr_evidence import closing_page_rows, verify_diff
from _storage import locked
from _triage import WORK_ROOT

BATCH_SIZE = 80
QUERY_WIDTHS = (1, 2, 4, 5, 8, 10, 16, 20, 32, 40, 64, 80)


def query(kind, phase, slots):
    # The operation shape is fixed at import time; item numbers are always variables.
    variables = ", ".join(["$owner: String!", "$repo: String!"] + [f"$n{i}: Int!" for i in range(slots)])
    field = "pullRequest" if kind == "pr" else "issue"
    aliases = "\n".join(f"p{i}: {field}(number: $n{i}) {{ ...Detail }}" for i in range(slots))
    common = "id databaseId number url state updatedAt"
    if kind == "pr":
        common += " baseRefOid headRefOid baseRefName headRefName baseRepository { id databaseId nameWithOwner } headRepository { id databaseId nameWithOwner } changedFiles additions deletions isDraft mergeable"
    if phase == "initial":
        common += " title body"
        common += " comments(first: 100) { totalCount pageInfo { hasNextPage endCursor } nodes { id databaseId url body createdAt updatedAt author { login } } }"
        if kind == "pr":
            common += " files(first: 100) { totalCount pageInfo { hasNextPage endCursor } nodes { path additions deletions changeType } }"
    else:
        common += " comments(first: 1) { totalCount }"

    if kind == "pr":
        common += " closingIssuesReferences(first: 100) { totalCount pageInfo { hasNextPage endCursor } nodes { id number url state updatedAt repository { id nameWithOwner } } }" if phase == "initial" else " closingIssuesReferences(first: 1) { totalCount }"

    return f"query BulkCorpus({variables}) {{ repository(owner: $owner, name: $repo) {{ id databaseId nameWithOwner {aliases} }} rateLimit {{ remaining resetAt }} }} fragment Detail on {'PullRequest' if kind == 'pr' else 'Issue'} {{ {common} }}"


QUERIES = {(kind, phase, slots): query(kind, phase, slots) for kind in ("pr", "issue") for phase in ("initial", "final") for slots in QUERY_WIDTHS}


def response(reader, cache, kind, phase, members):
    owner, name = cache.identity["full_name"].split("/", 1)
    slots = next(width for width in QUERY_WIDTHS if width >= len(members))
    variables = dict(owner=owner, repo=name)
    variables.update({f"n{i}": members[min(i, len(members) - 1)]["number"] for i in range(slots)})
    body, _ = reader._request("graphql", graphql_query=QUERIES[kind, phase, slots], graphql_variables=variables)
    data = reader._json(body)
    if not isinstance(data, dict) or not isinstance(data.get("data"), dict):
        raise ReadFailure("bulk GraphQL returned incomplete data")

    values = data["data"]
    limit = values.get("rateLimit") or {}
    if limit.get("remaining") == 0:
        reader.stopped = "GraphQL rate limit exhausted; check reset time before retrying"
        reader.throttle("graphql-exhausted", reset_at=limit.get("resetAt"))

    if data.get("errors"):
        if any(isinstance(error, dict) and error.get("type") == "RATE_LIMITED" for error in data["errors"]):
            reader.stopped = "GraphQL rate limit exhausted; check reset time before retrying"
            reader.throttle("graphql-exhausted", reset_at=limit.get("resetAt"))

        raise ReadFailure("bulk GraphQL returned errors; no member was published")

    raw_repo = values.get("repository")
    if not isinstance(raw_repo, dict):
        raise ReadFailure("bulk GraphQL repository is unavailable")

    observed = repository(raw_repo.get("nameWithOwner"), cache.identity["host"], raw_repo.get("databaseId"), raw_repo.get("id"))
    same_repository(cache.identity, observed)
    cache.bind_repository(observed)
    nodes = {}
    for i, member in enumerate(members):
        node = raw_repo.get(f"p{i}")
        if not isinstance(node, dict) or node.get("number") != member["number"]:
            raise ReadFailure("bulk GraphQL item is missing or changed")

        nodes[member["number"]] = node

    return observed, nodes


def mapped_summary(node, kind, observed):
    state = node["state"].lower()
    merged = state == "merged"
    result = dict(number=node["number"], id=node["databaseId"], node_id=node["id"], html_url=node["url"],
                  state="closed" if merged else state, merged=merged, title=node.get("title"), body=node.get("body"),
                  updated_at=node["updatedAt"], comments=node["comments"]["totalCount"])
    if kind == "pr":
        base_repo = node["baseRepository"]
        if not isinstance(base_repo, dict):
            raise ValueError("PR base repository is missing")

        base = dict(sha=node["baseRefOid"], ref=node["baseRefName"], repo=dict(full_name=base_repo["nameWithOwner"], id=base_repo["databaseId"], node_id=base_repo["id"]))
        head_repo = node.get("headRepository") or {}
        head = dict(sha=node["headRefOid"], ref=node["headRefName"], repo=dict(full_name=head_repo.get("nameWithOwner"), id=head_repo.get("databaseId"), node_id=head_repo.get("id")))
        result.update(base=base, head=head, changed_files=node["changedFiles"], additions=node["additions"], deletions=node["deletions"],
                      draft=node["isDraft"], mergeable={"MERGEABLE": True, "CONFLICTING": False}.get(node["mergeable"]))

    summary_identity(result, kind, node["number"], observed)
    return result


def comment_rows(node):
    connection = node["comments"]
    rows = []
    for comment in connection["nodes"] or []:
        rows.append(dict(id=comment["databaseId"], node_id=comment["id"], html_url=comment["url"], body=comment["body"],
                         user=dict(login=comment["author"]["login"]) if comment.get("author") else None,
                         created_at=comment["createdAt"], updated_at=comment["updatedAt"]))

    complete = not connection["pageInfo"]["hasNextPage"] and len(rows) == connection["totalCount"]
    return rows, complete


def git_heads(reader, cache, members):
    if reader.requests >= reader.budget or reader.stopped:
        raise ReadFailure(reader.stopped or "request budget exhausted before Git fetch")

    target = WORK_ROOT.parent
    probe = subprocess.run(["git", "-C", str(target), "rev-parse", "--show-toplevel"], capture_output=True, timeout=10)
    if probe.returncode or probe.stdout.decode().strip() != str(target):
        raise ReadFailure("install parent is not its Git checkout; local PR diffs unavailable")

    refs = [f"refs/pull/{member['number']}/head" for member in members]
    url = f"https://{cache.identity['host']}/{cache.identity['full_name']}.git"
    reader.requests += 1
    try:
        env = dict(os.environ, GIT_TERMINAL_PROMPT="0")
        result = subprocess.run(["git", "-C", str(target), "fetch", "--no-tags", "--no-write-fetch-head", url, *refs], capture_output=True, timeout=180, env=env)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ReadFailure(f"Git batch fetch failed ({type(error).__name__})") from error

    if result.returncode:
        raise ReadFailure("Git batch fetch failed; PR code remains incomplete")


def local_diff(node):
    target = WORK_ROOT.parent
    base, head = node["baseRefOid"], node["headRefOid"]
    for sha in (base, head):
        if not re.fullmatch(r"[0-9a-f]{40}", sha):
            raise ValueError("PR revision has an invalid Git SHA")

        found = subprocess.run(["git", "-C", str(target), "cat-file", "-e", sha + "^{commit}"], capture_output=True, timeout=10)
        if found.returncode:
            raise ValueError("PR base or head commit is unavailable locally")

    result = subprocess.run(["git", "-C", str(target), "diff", "--no-ext-diff", "--no-color", "--find-renames", "--unified=3", f"{base}...{head}"], capture_output=True, timeout=60)
    if result.returncode:
        raise ValueError("local PR diff failed")

    return result.stdout.decode("utf-8")


def file_rows(diff, node):
    by_path = {}
    for block in re.split(r"(?=^diff --git )", diff, flags=re.MULTILINE):
        if not block:
            continue

        header = block.splitlines()[0]
        match = re.fullmatch(r"diff --git a/([A-Za-z0-9_./-]+) b/([A-Za-z0-9_./-]+)", header)
        if not match:
            raise ValueError("Git diff contains an unsupported path")

        old, name = match.groups()
        if name in by_path:
            raise ValueError("Git diff contains a duplicate file")

        lines = block.splitlines()
        start = next((i for i, line in enumerate(lines) if line.startswith("@@")), None)
        if start is None:
            raise ValueError("Git diff omits a text hunk")

        status = "added" if "--- /dev/null" in lines[:start] else "removed" if "+++ /dev/null" in lines[:start] else "renamed" if old != name else "modified"
        by_path[name] = dict(filename=name, previous_filename=old, status=status, patch="\n".join(lines[start:]))

    connection = node["files"]
    rows = []
    for file in connection["nodes"] or []:
        path = file["path"]
        expected_status = {"ADDED": "added", "DELETED": "removed", "MODIFIED": "modified", "RENAMED": "renamed"}.get(file["changeType"])
        if expected_status is None or by_path.get(path, {}).get("status") != expected_status:
            raise ValueError("Git diff and GraphQL file change types differ")

        row = dict(by_path.get(path, {}), filename=path, additions=file["additions"], deletions=file["deletions"])
        rows.append(row)

    if connection["pageInfo"]["hasNextPage"] or len(rows) != connection["totalCount"] or set(by_path) != {row["filename"] for row in rows}:
        raise ValueError("Git diff and GraphQL file coverage differ or require another page")

    summary = dict(changed_files=node["changedFiles"], additions=node["additions"], deletions=node["deletions"])
    verify_diff(diff, rows, summary)
    return rows


def listed_files(node):
    """Keep a complete GraphQL path list usable when local diff verification cannot establish code coverage."""
    connection = node["files"]
    expected = connection["totalCount"]
    natural(expected, "file count")
    rows = []
    seen = set()
    statuses = {"ADDED": "added", "DELETED": "removed", "MODIFIED": "modified", "RENAMED": "renamed"}
    for file in connection["nodes"] or []:
        path = file["path"]
        if not isinstance(path, str) or not path or path in seen or file["changeType"] not in statuses:
            raise ValueError("GraphQL file list has a missing, repeated or unsupported path/change type")

        seen.add(path)
        natural(file["additions"], "file additions")
        natural(file["deletions"], "file deletions")
        rows.append(dict(filename=path, status=statuses[file["changeType"]], additions=file["additions"], deletions=file["deletions"]))

    complete = connection["pageInfo"]["hasNextPage"] is False and len(rows) == expected == node["changedFiles"]
    return rows, complete


def component(resource, revision, data, payloads, *, transport, complete=True, error=None, count=None, expected=None, format="json"):
    value = descriptor(resource, revision, "complete" if complete else "partial", None if complete else error)
    value["source"] = dict(transport=transport, resource=resource)
    value.update(expected_count=expected, received_count=count, pagination_complete=complete if count is not None else None)
    if format == "json":
        attach(value, data, payloads)
    else:
        ref = artifact_ref(data, format)
        value["object"] = ref
        payloads[object_name(ref)] = data

    return value


def acquire_batch(cache, reader, kind, members):
    if not members or len(members) > BATCH_SIZE or any(member["kind"] != kind for member in members):
        raise ValueError("invalid corpus batch")

    cache.initialize()
    with locked(cache.path("acquisition")):
        started = now()
        observed, initial = response(reader, cache, kind, "initial", members)
        git_error = None
        if kind == "pr":
            try:
                git_heads(reader, cache, members)
            except ReadFailure as error:
                git_error = str(error)

        final_repo, final = response(reader, cache, kind, "final", members)
        if git_error:
            reader.stopped = git_error

        same_repository(observed, final_repo)
        results = []
        for member in members:
            number = member["number"]
            first, last = initial[number], final[number]
            original = mapped_summary(first, kind, observed)
            identity, revision = summary_identity(original, kind, number, observed)
            latest = cache.latest(kind, number)
            if latest:
                same_item(latest[1]["identity"], identity)

            stable = all(first.get(key) == last.get(key) for key in ("id", "databaseId", "state", "updatedAt", "baseRefOid", "headRefOid", "changedFiles", "additions", "deletions")) and first["comments"]["totalCount"] == last["comments"]["totalCount"]
            payloads = {}
            resource = f"graphql repository/{cache.identity['full_name']}/{kind}/{number}"
            summary = component(resource, revision, original, payloads, transport="graphql", complete=stable, error="item changed during batch acquisition")
            comments, comments_complete = comment_rows(first)
            comment_value = component(resource + "/comments", revision, comments, payloads, transport="graphql", complete=stable and comments_complete,
                                      error="comments changed or require another page", count=len(comments), expected=first["comments"]["totalCount"])
            components = dict(summary=summary, comments=comment_value)
            if kind == "pr":
                initial_closing = first.get("closingIssuesReferences")
                final_closing = last.get("closingIssuesReferences")
                if initial_closing is None or final_closing is None:
                    components["closing_issues"] = component(resource + "/closing-issues", revision, [], payloads, transport="graphql",
                                                              complete=False, error="closing relationships unavailable in bulk response", count=0)
                else:
                    closing_count, closing, closing_info = closing_page_rows(observed, initial_closing, set())
                    if not isinstance(final_closing, dict):
                        raise ValueError("bulk closing relationship recheck is invalid")

                    final_closing_count = final_closing.get("totalCount")
                    natural(final_closing_count, "closing issue count")
                    closing_complete = not closing_info["hasNextPage"] and len(closing) == closing_count
                    closing_stable = closing_count == final_closing_count
                    closing_reason = "closing relationships changed, require another page, or item changed during batch acquisition"
                    components["closing_issues"] = component(resource + "/closing-issues", revision, closing, payloads, transport="graphql",
                                                              complete=stable and closing_stable and closing_complete, error=closing_reason,
                                                              count=len(closing), expected=closing_count)
                diff = None
                reason = git_error
                if reason is None:
                    try:
                        diff = local_diff(first)
                        files = file_rows(diff, first)
                    except (ValueError, UnicodeError, subprocess.TimeoutExpired) as error:
                        reason = str(error)

                if reason is None:
                    components["files"] = component(resource + "/files + git PR head", revision, files, payloads, transport="git", complete=stable,
                                                     error="item changed during batch acquisition", count=len(files), expected=first["files"]["totalCount"])
                    components["diff"] = component(resource + "/git-diff", revision, diff, payloads, transport="git", complete=stable,
                                                    error="item changed during batch acquisition", format="diff")
                else:
                    rows, listed_complete = listed_files(first)
                    files_complete = stable and listed_complete
                    files_reason = None if files_complete else "file list changed or requires another page"
                    components["files"] = component(resource + "/files", revision, rows, payloads, transport="graphql", complete=files_complete,
                                                     error=files_reason, count=len(rows), expected=first["files"]["totalCount"])
                    components["diff"] = component(resource + "/git-diff", revision, diff or "", payloads, transport="git", complete=False,
                                                    error=reason, format="diff")
            else:
                for name in ("files", "diff", "closing_issues"):
                    value = descriptor(resource, revision, "not_applicable", None)
                    value.update(fetched_at=None, source=None)
                    components[name] = value

            manifest = seal_snapshot(dict(schema_version=1, artifact="evidence-snapshot", repository=observed, started_at=started,
                                          completed_at=now(), requested_components=["summary", "comments", "files", "diff", "closing_issues"],
                                          items=[dict(identity=identity, revision=revision, components=components)]))
            cache.publish(manifest, payloads)
            results.append((member, manifest))

        return results
