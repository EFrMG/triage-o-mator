"""Pure external closure history v1 contracts; structural pins are not payload audits or authority.

`_closure_store` loads pinned manifests, checks repository/item/revision bindings, verifies selected object bytes and payload contracts, and matches selectors against raw rows. It requires a verified summary and retains component coverage gaps. None of the helpers here access files, acquire evidence, authenticate provenance, mutate watches, or grant review/approval.
"""

from copy import deepcopy
import hashlib

from _evidence import DIGEST, canonical, digest, fields, natural, text, timestamp, validate_item, validate_ref, validate_repository, validate_revision, version

ARTIFACT = "external-closure-history"
POLICY = "external-closure-v1"
KINDS = ("import", "observation", "correction", "competing")
PROVENANCE = ("unknown", "manual", "suspected-assistance", "confirmed-external-automation")


def checksum(value):
    return digest(canonical({key: item for key, item in value.items() if key != "checksum"}))


def hash_value(value, label):
    if not isinstance(value, str) or not DIGEST.fullmatch(value):
        raise ValueError(f"{label} must be a SHA-256 digest")


def nullable_text(value, label):
    if value is not None:
        text(value, label)


def bound_identity(repository, item):
    validate_repository(repository)
    validate_item(item)
    if any(value[key] is None for value in (repository, item) for key in ("database_id", "node_id")):
        raise ValueError("closure history requires fully bound repository and item identities")
    if item["kind"] != "pr":
        raise ValueError("closure history requires a PR")


def operation_id(repository, item, event_id):
    """A scoped closed-event identity, deliberately independent of content revision or observation time."""
    bound_identity(repository, item)
    natural(event_id, "closed event ID", 1)

    return digest(canonical(dict(policy=POLICY, repository=repository, item=item, event_id=event_id)))


def validate_source(source):
    fields(source, ("component", "offset", "source_id", "source_digest", "object", "event", "created_at", "updated_at"))
    if source["component"] not in ("summary", "comments", "timeline"):
        raise ValueError("unsupported closure source component")
    natural(source["offset"], "source offset")
    hash_value(source["source_digest"], "source digest")
    validate_ref(source["object"])
    if source["object"]["format"] != "json":
        raise ValueError("closure source requires a JSON object")

    if source["component"] == "summary":
        if source["offset"] != 0 or source["source_id"] is not None or source["event"] is not None:
            raise ValueError("summary selector requires offset zero and null source/event IDs")
    else:
        natural(source["source_id"], "source ID", 1)
        if source["component"] == "comments" and source["event"] is not None:
            raise ValueError("comment selector cannot declare a timeline event")
        if source["component"] == "timeline":
            text(source["event"], "timeline event")

    for key in ("created_at", "updated_at"):
        if source[key] is not None:
            timestamp(source[key])


def source_pin(component, offset, object_ref, raw):
    """Build a structural selector from an already selected raw row; caller still owes manifest/payload validation."""
    if not isinstance(raw, dict):
        raise ValueError("source row must be an object")

    result = dict(component=component, offset=offset, source_id=None if component == "summary" else raw.get("id"),
                  source_digest=digest(canonical(raw)), object=deepcopy(object_ref),
                  event=raw.get("event") if component == "timeline" else None,
                  created_at=raw.get("created_at"), updated_at=raw.get("updated_at"))
    validate_source(result)

    return result


def validate_observation(observation, repository, item):
    fields(observation, ("snapshot_id", "observed_at", "revision", "state", "closed_at", "sources", "closure_source", "operation_id", "gaps"))
    hash_value(observation["snapshot_id"], "snapshot ID")
    timestamp(observation["observed_at"])
    validate_revision(observation["revision"])
    if observation["state"] not in ("open", "closed", "merged"):
        raise ValueError("closure observation requires an explicit open, closed or merged summary")
    if observation["closed_at"] is not None:
        timestamp(observation["closed_at"])

    sources = observation["sources"]
    if not isinstance(sources, list) or not sources:
        raise ValueError("observation requires pinned sources")

    selectors, objects = set(), {}
    summaries = 0
    for source in sources:
        validate_source(source)
        selector = (source["component"], source["offset"])
        if selector in selectors:
            raise ValueError("duplicate source selector in one observation")
        selectors.add(selector)
        component = source["component"]
        if component in objects and objects[component] != source["object"]:
            raise ValueError("one observation must use one object per source component")
        objects[component] = source["object"]
        summaries += source["component"] == "summary"

    if summaries != 1:
        raise ValueError("observation requires exactly one pinned summary")

    gaps = observation["gaps"]
    if not isinstance(gaps, list):
        raise ValueError("observation gaps must be a list")
    for gap in gaps:
        fields(gap, ("component", "reason"))
        if gap["component"] not in ("summary", "comments", "timeline", "operation"):
            raise ValueError("unsupported gap component")
        text(gap["reason"], "gap reason")

    closure = observation["closure_source"]
    if closure is None:
        if observation["operation_id"] is not None or observation["state"] != "closed":
            raise ValueError("summary-only closure requires closed state and unknown operation")
        if not any(gap["component"] == "operation" for gap in gaps):
            raise ValueError("summary-only closure requires an explicit operation identity gap")
    else:
        natural(closure, "closure source index")
        if closure >= len(sources):
            raise ValueError("closure source index is outside pinned sources")
        source = sources[closure]
        if source["component"] != "timeline" or source["event"] != "closed":
            raise ValueError("closure source must select a closed timeline event")
        if observation["operation_id"] != operation_id(repository, item, source["source_id"]):
            raise ValueError("closure operation identity mismatch")


def validate_claim(claim, observation, item):
    fields(claim, ("external", "actor", "run_id", "rationale", "survivor", "provenance", "supports"))
    if claim["external"] is not None:
        fields(claim["external"], ("namespace", "record_id"))
        text(claim["external"]["namespace"], "external namespace")
        text(claim["external"]["record_id"], "external record ID")

    for key in ("actor", "run_id", "rationale"):
        nullable_text(claim[key], key)

    if claim["survivor"] is not None:
        natural(claim["survivor"], "supplied same-repository survivor PR", 1)
        if claim["survivor"] == item["number"]:
            raise ValueError("survivor must be another PR")
    if claim["provenance"] not in PROVENANCE:
        raise ValueError("unsupported attributed provenance classification")
    if not isinstance(claim["supports"], list):
        raise ValueError("claim supports must be source indexes")

    seen = set()
    for support in claim["supports"]:
        natural(support, "claim supporting source index")
        if support >= len(observation["sources"]) or support in seen:
            raise ValueError("invalid or repeated claim supporting source index")
        seen.add(support)

    if claim["provenance"] == "confirmed-external-automation" and not seen:
        raise ValueError("confirmed automation claim requires attributed supporting sources")


def content_digest(repository, item, by, observation, claim, *, kind="import", predecessor=None, reason=None):
    """Request equality binds explicit intent, excluding only local ingestion time and expected history checksum."""
    return digest(canonical(dict(policy=POLICY, repository=repository, item=item, by=by, observation=observation, claim=claim,
                                 kind=kind, predecessor=predecessor, reason=reason)))


def create_history(repository, item):
    bound_identity(repository, item)
    result = dict(schema_version=1, artifact=ARTIFACT, policy=POLICY, repository=deepcopy(repository), item=deepcopy(item), entries=[])
    result["checksum"] = checksum(result)

    return result


def validate_history(history):
    fields(history, ("schema_version", "artifact", "policy", "repository", "item", "entries", "checksum"))
    version(history, ARTIFACT)
    if history["policy"] != POLICY:
        raise ValueError("unsupported external closure policy")
    bound_identity(history["repository"], history["item"])
    if history["checksum"] != checksum(history):
        raise ValueError("closure history checksum mismatch")
    if not isinstance(history["entries"], list):
        raise ValueError("closure entries must be a list")

    # Copy the incremental hash before appending each entry to verify full-prefix checksums in linear byte work.
    empty = create_history(history["repository"], history["item"])
    encoded = canonical({key: value for key, value in empty.items() if key != "checksum"})
    before, _, after = encoded.partition('"entries":[]')
    running = hashlib.sha256((before + '"entries":[').encode("utf-8"))
    suffix = ("]" + after).encode("utf-8")
    seen, contents, corrected, external_ids = {}, set(), set(), set()
    for entry in history["entries"]:
        fields(entry, ("id", "content_digest", "kind", "at", "by", "reason", "predecessor", "base_checksum", "observation", "claim"))
        if entry["kind"] not in KINDS:
            raise ValueError("unsupported closure entry kind")
        timestamp(entry["at"])
        text(entry["by"], "import attribution")
        nullable_text(entry["reason"], "change reason")
        validate_observation(entry["observation"], history["repository"], history["item"])
        validate_claim(entry["claim"], entry["observation"], history["item"])
        expected = content_digest(history["repository"], history["item"], entry["by"], entry["observation"], entry["claim"],
                                  kind=entry["kind"], predecessor=entry["predecessor"], reason=entry["reason"])
        if entry["content_digest"] != expected or expected in contents:
            raise ValueError("closure content digest mismatch or repeated import")
        if entry["id"] != digest(canonical({key: value for key, value in entry.items() if key != "id"})):
            raise ValueError("closure entry identity mismatch")
        prefix_hash = running.copy()
        prefix_hash.update(suffix)
        if entry["base_checksum"] != prefix_hash.hexdigest():
            raise ValueError("closure entry does not bind its complete predecessor history")

        previous = seen.get(entry["predecessor"]) if isinstance(entry["predecessor"], str) else None
        external = entry["claim"]["external"]
        external_key = canonical(external) if external is not None else None
        if entry["kind"] == "import":
            if entry["predecessor"] is not None or entry["reason"] is not None or (external_key is not None and external_key in external_ids):
                raise ValueError("changed external record conflicts; append an explicit attributed change")
        else:
            text(entry["reason"], "change reason")
            if previous is None:
                raise ValueError("change requires an earlier explicit predecessor")
            if previous["claim"]["external"] != external:
                raise ValueError("change must retain predecessor external identity")
            if entry["kind"] == "correction":
                if previous["id"] in corrected:
                    raise ValueError("correction predecessor is already corrected")
                corrected.add(previous["id"])
            if entry["kind"] == "observation":
                if entry["claim"] != previous["claim"] or entry["observation"]["operation_id"] != previous["observation"]["operation_id"]:
                    raise ValueError("new observation cannot correct claims or merge operation identities")

        if seen:
            running.update(b",")
        running.update(canonical(entry).encode("utf-8"))
        seen[entry["id"]] = entry
        contents.add(expected)
        if external_key is not None:
            external_ids.add(external_key)

    return history


def append_entry(history, observation, claim, *, by, at, checkpoint, kind="import", predecessor=None, reason=None):
    """Return a new validated history, or an unchanged copy for an exact retry; never mutate inputs.

    A repeated content digest is recognized even after a successful write invalidated the caller's checkpoint. Every new entry requires the current full history checksum. Persistence must re-run this helper while holding its own transaction lock.
    """
    validate_history(history)
    text(by, "import attribution")
    timestamp(at)
    validate_observation(observation, history["repository"], history["item"])
    validate_claim(claim, observation, history["item"])
    if kind not in KINDS:
        raise ValueError("unsupported closure entry kind")

    content = content_digest(history["repository"], history["item"], by, observation, claim, kind=kind, predecessor=predecessor, reason=reason)
    if any(entry["content_digest"] == content for entry in history["entries"]):
        return deepcopy(history)
    if checkpoint != history["checksum"]:
        raise ValueError("closure history checkpoint changed; inspect before saving")

    result = deepcopy(history)
    entry = dict(content_digest=content, kind=kind, at=at, by=by, reason=reason, predecessor=predecessor,
                 base_checksum=checkpoint, observation=deepcopy(observation), claim=deepcopy(claim))
    entry["id"] = digest(canonical(entry))
    result["entries"].append(entry)
    result["checksum"] = checksum(result)
    validate_history(result)

    return result
