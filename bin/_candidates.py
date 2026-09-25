"""Pinned offline PR discovery: direct signals propose review sets, never redundancy."""

import json
from collections import Counter, defaultdict
from itertools import combinations
from datetime import datetime, timezone

from _chunks import corpus_listing, page, window
from _evidence import DEFAULT_MAX_AGE, canonical, component_problems, digest, fields, natural, repository, same_repository, text, timestamp, validate_payload, validate_item, validate_repository
from _similar import TitleIndex

POLICY = "pr-candidate-sets-v1"
COMPONENTS = ("summary", "files", "closing_issues")
MAX_MEMBERS = 5000
MAX_BYTES = 256 * 1024 * 1024
MAX_FEATURES = 1000
SIGNAL_PREVIEW = 10
BROAD_PATHS = {"package-lock.json", "yarn.lock", "pnpm-lock.yaml", "Cargo.lock", "go.sum", "poetry.lock", "uv.lock", "Gemfile.lock", "composer.lock"}


def scope(cache, snapshot, corpus):
    if bool(snapshot) == bool(corpus):
        raise ValueError("select exactly one snapshot or corpus")

    if snapshot:
        manifest = cache.load_manifest(snapshot)
        members = [dict(identity=row["identity"], snapshot_id=snapshot, record=row) for row in manifest["items"]]
        source = dict(repository=manifest["repository"], snapshot=snapshot, corpus=None, corpus_checkpoint=None)
    else:
        from _corpus import PROFILES, key, load_plan, load_state, observed_state, pinned
        from _reader import problems

        plan = load_plan(cache, corpus, metadata_only=True)
        if len(plan["members"]) > MAX_MEMBERS:
            raise ValueError(f"candidate scope exceeds {MAX_MEMBERS} members; select a smaller frozen snapshot or corpus")

        state = observed_state(cache, corpus, plan, load_state(cache, corpus, plan, metadata_only=True))
        # An unstarted corpus has no persisted checkpoint time; mirror corpus-list's stable pending token.
        if state["last_run"] is None and state["status"] == "pending" and all(entry["attempts"] == 0 for entry in state["items"].values()):
            state["updated_at"] = None

        members = []
        for identity in plan["members"]:
            entry = state["items"][key(identity)]
            record = pinned(cache, entry["snapshot_id"], identity, metadata_only=True) if entry["snapshot_id"] else None
            if entry["outcome"] == "complete" and any(problem != "observation outside freshness window" for values in problems(record, PROFILES[plan["profile"]], plan["max_age"]).values() for problem in values):
                raise ValueError("complete corpus member references incomplete evidence")

            members.append(dict(identity=identity, snapshot_id=entry["snapshot_id"], record=record))

        source = dict(repository=plan["repository"], snapshot=None, corpus=corpus, corpus_checkpoint=digest(canonical(state)))

    if len(members) > MAX_MEMBERS:
        raise ValueError(f"candidate scope exceeds {MAX_MEMBERS} members; select a smaller frozen snapshot or corpus")

    size = sum(descriptor["object"]["bytes"] for member in members if member.get("record") and member["identity"]["kind"] == "pr"
               for name in COMPONENTS if (descriptor := member["record"]["components"].get(name)) and descriptor["object"])
    if size > MAX_BYTES:
        raise ValueError("candidate discovery payload budget exceeds 256 MiB; select a smaller frozen scope")

    return source, sorted(members, key=lambda row: (row["identity"]["kind"], row["identity"]["number"]))


def observe(cache, member, repo, as_of, max_age):
    record = member.get("record")
    result = dict(item=member["identity"], snapshot_id=member["snapshot_id"], revision=record["revision"] if record else None,
                  components={}, exclusions=[], holds=["semantic-comparison-required"], title=None, base_branch=None)
    features = dict(files=set(), closing_issues=set())
    if member["identity"]["kind"] != "pr":
        result["exclusions"].append("issue-policy-unsupported")
        return result, features

    payloads = {}
    for name in COMPONENTS:
        descriptor = record["components"].get(name) if record else None
        problems = component_problems(name, descriptor, record["revision"], as_of, max_age) if descriptor else ["missing"]
        old = descriptor and descriptor["fetched_at"] and (timestamp(as_of) - timestamp(descriptor["fetched_at"])).total_seconds() > max_age
        stale = old and "observation outside freshness window" in problems
        if stale:
            problems.remove("observation outside freshness window")
        result["components"][name] = dict(descriptor=descriptor, problems=problems, payload_verified=False)
        if stale:
            result["holds"].append(f"discovery-evidence-stale:{name}")
        if descriptor and descriptor["object"]:
            try:
                payload = cache.read_object(descriptor["object"])
            except FileNotFoundError:
                problems.append("payload unavailable")
            else:
                validate_payload(name, descriptor, "pr", payload)
                result["components"][name]["payload_verified"] = True
                payloads[name] = json.loads(payload)

        if problems:
            result["holds"].append(f"discovery-evidence-gap:{name}")

    summary = payloads.get("summary", {})
    if result["components"]["summary"]["problems"] or not summary:
        result["exclusions"].append("summary-unavailable-or-not-current-at-evaluation")
        return result, features

    identity, revision = record["identity"], record["revision"]
    result["item"] = identity
    base, head = summary.get("base") or {}, summary.get("head") or {}
    if not isinstance(base, dict):
        raise ValueError("invalid discovery summary base")

    raw_repo = base.get("repo") or {}
    if not isinstance(head, dict) or not isinstance(raw_repo, dict):
        raise ValueError("invalid discovery summary base/head")

    if any(repo[name] is None or identity[name] is None for name in ("database_id", "node_id")):
        result["exclusions"].append("unbound-repository-or-item")
    if summary.get("number") != identity["number"] or summary.get("id") != identity["database_id"] or summary.get("node_id") != identity["node_id"]:
        result["exclusions"].append("summary-identity-mismatch")
    if not isinstance(base, dict) or not all(revision.values()) or (summary.get("updated_at"), base.get("sha"), head.get("sha")) != (revision["updated_at"], revision["base_sha"], revision["head_sha"]):
        result["exclusions"].append("summary-revision-unverified")
    if summary.get("state") != "open":
        result["exclusions"].append("not-observed-open")
    if not all(raw_repo.get(key) for key in ("full_name", "id", "node_id")) or not isinstance(base.get("ref"), str) or not base["ref"].strip():
        result["exclusions"].append("base-identity-unknown")
    else:
        observed = repository(raw_repo["full_name"], host=repo["host"], database_id=raw_repo["id"], node_id=raw_repo["node_id"])
        same_repository(repo, observed)
        result["base_branch"] = base["ref"]

    title = summary.get("title")
    result["title"] = title if isinstance(title, str) else None
    if result["title"] is None:
        result["holds"].append("title-unavailable")

    for name in ("files", "closing_issues"):
        values = payloads.get(name, [])
        if result["components"][name]["problems"]:
            continue

        if len(values) > MAX_FEATURES:
            result["holds"].append(f"discovery-feature-limit:{name}")
            continue

        for value in values:
            if name == "files":
                path = value.get("filename") if isinstance(value, dict) else None
                text(path, "discovery filename")
                features[name].add(path)
            else:
                linked_repo, linked = value["repository"], value["identity"]
                validate_repository(linked_repo)
                validate_item(linked)
                if linked_repo["full_name"] == repo["full_name"]:
                    same_repository(dict(repo, database_id=None), linked_repo)

                natural(linked["number"], "closing issue number", 1)
                text(linked["node_id"], "closing issue node ID")
                text(linked_repo["node_id"], "closing issue repository ID")
                if linked["kind"] != "issue" or linked_repo["host"] != repo["host"] or value["url"] != f"https://{repo['host']}/{linked_repo['full_name']}/issues/{linked['number']}":
                    raise ValueError("invalid closing issue discovery identity")

                features[name].add(canonical(dict(repository=linked_repo, identity=linked)))

    return result, features


def broad_path(path):
    return path.rsplit("/", 1)[-1] in BROAD_PATHS or any(part in ("vendor", "generated", "node_modules", "dist") for part in path.split("/"))


def discover(cache, snapshot=None, corpus=None, offset=0, limit=20, checkpoint=None, max_age=DEFAULT_MAX_AGE, as_of=None,
             title_threshold=0.8, max_frequency=10):
    window(offset, limit, 100)
    natural(max_age, "maximum age")
    natural(max_frequency, "maximum signal frequency", 2)
    if type(title_threshold) not in (int, float) or not 0 < title_threshold <= 1:
        raise ValueError("title threshold must be greater than zero and at most one")
    if offset and (checkpoint is None or as_of is None):
        raise ValueError("candidate continuation requires checkpoint and as-of")

    as_of = as_of or datetime.now(timezone.utc).isoformat()
    timestamp(as_of)
    source, members = scope(cache, snapshot, corpus)
    from _notdupes import load as negative_verdicts, record_key

    all_negatives = negative_verdicts()
    numbers = {member["identity"]["number"] for member in members if member["identity"]["kind"] == "pr"}
    negatives, excluded = [], {}
    for verdict in all_negatives:
        if verdict.get("kind") not in ("pr", "issue"):
            raise ValueError("invalid negative verdict kind")
        for key in ("a", "b"):
            natural(verdict.get(key), "negative verdict number", 1)

        if verdict["kind"] == "pr" and verdict["a"] in numbers and verdict["b"] in numbers:
            key = record_key(verdict)
            if key in excluded or verdict["a"] == verdict["b"]:
                raise ValueError("duplicate or self-paired negative verdict")

            negatives.append(verdict)
            excluded[key] = [verdict]

    observations, features = [], {}
    for member in members:
        observed, signals = observe(cache, member, source["repository"], as_of, max_age)
        observations.append(observed)
        if not observed["exclusions"]:
            features[observed["item"]["number"]] = signals

    records = {(member["identity"]["kind"], member["identity"]["number"]): member["record"] for member in members}
    usable = {row["item"]["number"]: row for row in observations if not row["exclusions"]}
    frequencies = {name: Counter(value for signals in features.values() for value in signals[name]) for name in ("files", "closing_issues")}
    index = TitleIndex([dict(kind="pr", number=number, title=row["title"] or "") for number, row in usable.items()])
    title_pairs = set()
    omitted_title_terms = 0
    for term, posting in index.postings.items():
        if len(posting) > max(MAX_MEMBERS // 25, max_frequency * 10):
            omitted_title_terms += 1
            continue

        for (left_key, _), (right_key, _) in combinations(posting, 2):
            left, right = sorted((left_key[1], right_key[1]))
            title_pairs.add((left, right))

    titles = {}
    for left, right in title_pairs:
        left_vec, right_vec = index.vectors[("pr", left)], index.vectors[("pr", right)]
        score = sum(weight * right_vec.get(term, 0) for term, weight in left_vec.items())
        if score >= title_threshold:
            titles[left, right] = round(score, 3)
    pair_signals = defaultdict(list)
    for pair, score in titles.items():
        pair_signals[pair].append(dict(signal="title", score=score))

    for name in ("files", "closing_issues"):
        postings = defaultdict(list)
        for number, signals in features.items():
            for value in signals[name]:
                if frequencies[name][value] <= max_frequency and (name != "files" or not broad_path(value)):
                    postings[value].append(number)

        for value in sorted(postings):
            for left, right in combinations(sorted(postings[value]), 2):
                pair_signals[left, right].append(dict(signal=name, value=json.loads(value) if name == "closing_issues" else value,
                                                      frequency=frequencies[name][value]))

    edges, rejected, suppressed = {}, [], []
    for (left, right), reasons in sorted(pair_signals.items()):

        pair = dict(members=[left, right], signals=reasons[:SIGNAL_PREVIEW], omitted_signals=max(0, len(reasons) - SIGNAL_PREVIEW))
        verdicts = excluded.get(("pr", left, right), [])
        if verdicts:
            pair["negative_verdicts"] = verdicts
        if verdicts or usable[left]["base_branch"] != usable[right]["base_branch"]:
            rejected.append(dict(type="excluded-pair", **pair, reasons=(["recorded-negative-verdict"] if verdicts else []) +
                                 (["different-base-branches"] if usable[left]["base_branch"] != usable[right]["base_branch"] else [])))
        else:
            edges[left, right] = pair

    for name, counts in frequencies.items():
        values = [dict(value=json.loads(value) if name == "closing_issues" else value, frequency=count,
                       reasons=(["frequent-in-selected-scope"] if count > max_frequency else []) +
                               (["known-broad-path"] if name == "files" and broad_path(value) else []))
                  for value, count in sorted(counts.items()) if count > max_frequency or (name == "files" and broad_path(value))]
        suppressed.append(dict(signal=name, total=len(values), preview=values[:SIGNAL_PREVIEW], omitted=max(0, len(values) - SIGNAL_PREVIEW)))

    if omitted_title_terms:
        suppressed.append(dict(signal="title-terms", total=omitted_title_terms, preview=[], omitted=omitted_title_terms))

    # Greedy complete-link edge cover: overlapping review sets retain every direct edge, without graph-transitive inference.
    remaining = set(edges)
    suggestions = []
    while remaining:
        selected = list(min(remaining))
        for number in sorted(usable):
            if number not in selected and all(tuple(sorted((number, other))) in edges for other in selected):
                selected.append(number)

        selected.sort()
        pairs = list(combinations(selected, 2))
        remaining.difference_update(pairs)
        rows = [usable[number] for number in selected]
        holds = sorted({hold for row in rows for hold in row["holds"]})
        if len({row["revision"]["base_sha"] for row in rows}) > 1:
            holds.append("base-revisions-require-reconciliation")

        suggestion = dict(type="candidate-set", members=selected, base_branch=rows[0]["base_branch"],
                          evidence=rows, pairs=[edges[pair] for pair in pairs], holds=holds,
                          relationship="unassessed", survivor=None)
        suggestions.append(dict(suggestion, id=digest(canonical(suggestion))))

    options = dict(snapshot=snapshot, corpus=corpus, max_age=max_age, as_of=as_of,
                   title_threshold=title_threshold, max_frequency=max_frequency)
    evaluated = dict(policy=POLICY, source=source, options=options, observations=observations, negative_verdicts=negatives,
                     suppressed_signals=suppressed, results=suggestions + rejected)
    token = digest(canonical(evaluated))
    if checkpoint is not None and checkpoint != token:
        raise ValueError("candidate inputs, options or exclusions changed; restart discovery")
    if corpus and corpus_listing(cache, corpus, 0, 1)["checkpoint"] != source["corpus_checkpoint"]:
        raise ValueError("corpus checkpoint changed during candidate discovery")
    if all_negatives != negative_verdicts():
        raise ValueError("negative verdicts changed during candidate discovery")

    results = evaluated.pop("results")
    selected = results[offset:offset + limit]
    pagination = page(len(results), offset, len(selected))
    return dict(schema_version=1, **evaluated, checkpoint=token, results=selected, pagination=pagination,
                candidate_sets=len(suggestions), excluded_pairs=len(rejected),
                continuation=dict(options, offset=pagination["next_offset"], limit=limit, checkpoint=token) if pagination["next_offset"] is not None else None,
                requests=0, mode="offline", limits=dict(members=MAX_MEMBERS, payload_bytes=MAX_BYTES, features_per_component=MAX_FEATURES, signal_preview=SIGNAL_PREVIEW),
                meaning="Discovery proposals only. Every pair has a direct signal, never proven redundancy. No survivor, approval or closure authority.",
                coverage="All members of this bounded frozen scope examined; gaps and suppressed signals prevent any exhaustive backlog or negative verdict claim.")


def compact_report(report):
    """Keep page proposals and their IDs while bounding all-scope diagnostics for an agent handoff."""
    counts = Counter()
    exclusions = Counter()
    holds = Counter()
    for row in report["observations"]:
        counts["examined"] += 1
        counts["eligible" if not row["exclusions"] else "excluded"] += 1
        exclusions.update(row["exclusions"])
        holds.update(row["holds"])

    results = []
    for row in report["results"]:
        if row["type"] != "candidate-set" or len(row["pairs"]) <= 20:
            results.append(row)
            continue

        results.append(dict(row, pairs=row["pairs"][:20], pair_count=len(row["pairs"]), omitted_pairs=len(row["pairs"]) - 20))

    return dict(report, observations=None, negative_verdicts=None,
                observation_counts=dict(counts), exclusion_counts=dict(sorted(exclusions.items())),
                hold_counts=dict(sorted(holds.items())), negative_verdict_count=len(report["negative_verdicts"]), results=results,
                compact=True,
                compact_notice="All-scope observations and negative-verdict records are summarized; selected pair lists may be truncated. IDs bind the full revalidated proposal.")


def create_from_packet(packet, identifier, by):
    from _cache import EvidenceCache
    from _triage import REPO
    from _groups import create_group

    if type(packet.get("schema_version")) is not int or packet.get("schema_version") != 1 or packet.get("policy") != POLICY:
        raise ValueError("unsupported candidate discovery packet")

    if packet["source"]["repository"]["full_name"] != REPO:
        raise ValueError("candidate packet repository does not match install")

    cache = EvidenceCache(packet["source"]["repository"])
    # Re-evaluate pinned evidence and current exclusions; never trust an edited report as a verified proposal.
    report = discover(cache, **packet["options"], offset=packet["pagination"]["offset"], limit=packet["pagination"]["returned"] or 1,
                      checkpoint=packet["checkpoint"])
    suggestion = next((row for row in report["results"] if row.get("id") == identifier), None)
    if suggestion is None:
        raise ValueError("candidate ID is not on the selected discovery page")

    group = create_group("Candidate PR set: " + ", ".join(f"#{number}" for number in suggestion["members"]),
                         "Pinned discovery signals; semantic comparison and preservation review required.", "", by)
    group["members"] = [dict(kind="pr", number=number, notes="Candidate only; no duplicate verdict.",
                             added_by=by, added_at=group["created_at"], updated_by=by, updated_at=group["created_at"])
                        for number in suggestion["members"]]
    origin = dict(schema_version=1, policy=POLICY, source=report["source"], options=report["options"], checkpoint=report["checkpoint"],
                  suggestion=suggestion, by=by, at=group["created_at"])
    group["candidate_origin"] = dict(origin, digest=digest(canonical(origin)))
    return group


def validate_origin(group):
    origin = group["candidate_origin"]
    fields(origin, ("schema_version", "policy", "source", "options", "checkpoint", "suggestion", "by", "at", "digest"))
    if type(origin["schema_version"]) is not int or origin["schema_version"] != 1 or origin["policy"] != POLICY:
        raise ValueError("unsupported candidate origin")
    if origin["digest"] != digest(canonical({key: value for key, value in origin.items() if key != "digest"})):
        raise ValueError("candidate origin checksum mismatch")
    if origin["source"]["repository"]["full_name"] != group["repo"]:
        raise ValueError("candidate origin repository mismatch")

    text(origin["by"], "candidate creator")
    timestamp(origin["at"])
    suggestion = origin["suggestion"]
    if suggestion["id"] != digest(canonical({key: value for key, value in suggestion.items() if key != "id"})):
        raise ValueError("candidate suggestion checksum mismatch")
