"""Read and validate saved PR closure proposals; publication and changes remain in bin/auto-close."""

import json
import re
import uuid

from _evidence import canonical, digest
from _action_proposal_records import valid_decision_hold
from _triage import DATA_DIR

FEEDBACK_POLICY = "auto-close-feedback-v1"


def proposal_path(number):
    return DATA_DIR / "auto-close" / f"pr-{number}.json"


def valid_rejection(value):
    rejection = value.get("rejection")
    if not isinstance(rejection, dict):
        return False

    by, reason, at, checkpoint = (rejection.get(field) for field in ("by", "reason", "at", "proposal_checkpoint"))
    if not isinstance(by, str) or not by.strip() or not isinstance(reason, str) or len(reason) > 10000:
        return False
    if not isinstance(at, str) or not re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", at):
        return False
    if not isinstance(checkpoint, str) or not re.fullmatch(r"[0-9a-f]{64}", checkpoint):
        return False

    history = value.get("history")
    if not isinstance(history, list) or not history or not isinstance(history[-1], dict):
        return False

    previous = history[-1]
    return previous.get("checksum") == checkpoint and previous.get("status") == "pending" and previous.get("request_id") == value.get("request_id") and value.get("outcome") is None


def valid_reconsideration(value):
    history = value.get("history", [])
    if not isinstance(history, list):
        return False

    rejected_index = next((index for index in range(len(history) - 1, -1, -1)
                           if isinstance(history[index], dict) and history[index].get("status") == "rejected"), None)
    reconsideration = value.get("reconsideration")
    if rejected_index is None:
        return reconsideration is None
    if not isinstance(reconsideration, dict):
        return False

    rejected = history[rejected_index]
    prior = history[rejected_index - 1] if rejected_index else None
    rejection = rejected.get("rejection")
    by, reason, at, checkpoint = (reconsideration.get(field) for field in ("by", "reason", "at", "rejected_checkpoint"))
    return (isinstance(by, str) and bool(by.strip()) and len(by) <= 200 and
            isinstance(reason, str) and bool(reason.strip()) and len(reason) <= 10000 and
            isinstance(at, str) and bool(re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", at)) and
            isinstance(checkpoint, str) and checkpoint == rejected.get("checksum") and
            isinstance(rejection, dict) and isinstance(prior, dict) and prior.get("status") == "pending" and
            rejection.get("proposal_checkpoint") == prior.get("checksum") and rejected.get("request_id") == prior.get("request_id"))


def load(path, repo):
    if path.parent.is_symlink() or path.is_symlink() or not path.is_file():
        raise ValueError(f"unavailable proposal: {path.name}")
    value = json.loads(path.read_text(encoding="utf-8"))
    checksum = value.pop("checksum", None)
    if not isinstance(checksum, str) or digest(canonical(value)) != checksum:
        raise ValueError(f"proposal checksum changed: {path.name}")
    value["checksum"] = checksum
    if value.get("artifact") != "auto-close-proposal" or value.get("schema_version") != 1 or value.get("repo") != repo or value.get("number") != int(path.stem.removeprefix("pr-")) or value.get("status") not in ("pending", "rejected", "executed", "uncertain"):
        raise ValueError(f"invalid proposal identity or status: {path.name}")
    if value.get("target") != f"https://{value.get('host')}/{repo}/pull/{value['number']}":
        raise ValueError(f"invalid proposal target: {path.name}")
    if not valid_decision_hold(value):
        raise ValueError(f"invalid human decision question: {path.name}")
    if value["status"] == "rejected":
        if not valid_rejection(value):
            raise ValueError(f"invalid proposal rejection: {path.name}")
    elif value.get("rejection") is not None:
        raise ValueError(f"rejection on non-rejected proposal: {path.name}")
    if not valid_reconsideration(value):
        raise ValueError(f"invalid proposal reconsideration: {path.name}")

    return value


def feedback(number, repo, completed_batch=None):
    path = proposal_path(number)
    if path.parent.is_symlink():
        raise ValueError("invalid proposal directory")
    if not path.exists() and not path.is_symlink():
        return dict(checkpoint=None, events=[])

    proposal = load(path, repo)
    history = proposal.get("history", [])
    if not isinstance(history, list):
        raise ValueError("invalid proposal history")

    events = []
    for version in [*history, proposal]:
        if not isinstance(version, dict) or version.get("repo") != repo or version.get("number") != number:
            raise ValueError("invalid historical proposal identity")
        if not isinstance(version.get("request_id"), str) or not isinstance(version.get("target"), str):
            raise ValueError("invalid historical proposal request or target")
        if version["target"] != f"https://{version.get('host')}/{repo}/pull/{number}":
            raise ValueError("invalid historical proposal target")
        try:
            if str(uuid.UUID(version["request_id"])) != version["request_id"]:
                raise ValueError("noncanonical request ID")
        except ValueError as error:
            raise ValueError("invalid historical proposal request ID") from error

        status = version.get("status")
        if status == "rejected":
            rejection = version.get("rejection")
            if not isinstance(rejection, dict) or not all(isinstance(rejection.get(field), str) and rejection[field].strip() for field in ("by", "at", "proposal_checkpoint")) or not isinstance(rejection.get("reason"), str):
                raise ValueError("invalid historical proposal rejection")
            if not re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", rejection["at"]) or not re.fullmatch(r"[0-9a-f]{64}", rejection["proposal_checkpoint"]):
                raise ValueError("invalid historical rejection reference")

            events.append(dict(kind="rejection", by=rejection["by"], at=rejection["at"], reason=rejection["reason"],
                               proposal_checkpoint=rejection["proposal_checkpoint"], request_id=version.get("request_id"),
                               target=version.get("target")))
        elif status in ("executed", "uncertain"):
            outcome = version.get("outcome")
            if not isinstance(outcome, dict) or outcome.get("request_id") != version["request_id"] or not isinstance(outcome.get("comment"), dict) or not isinstance(outcome.get("state_change"), dict):
                raise ValueError("invalid historical proposal outcome")

            comment, state = outcome["comment"], outcome["state_change"]
            if not isinstance(comment.get("status"), str) or not isinstance(state.get("status"), str):
                raise ValueError("invalid historical proposal outcome status")
            if any(value is not None and not isinstance(value, str) for value in (comment.get("url"), state.get("url"), comment.get("error"), state.get("error"))):
                raise ValueError("invalid historical proposal outcome details")

            events.append(dict(kind="write_outcome", status=status, request_id=version.get("request_id"), target=version.get("target"),
                               comment_status=comment["status"], state_status=state["status"],
                               comment_url=comment.get("url"), state_url=state.get("url"),
                               comment_error=comment.get("error"), state_error=state.get("error")))
        elif status != "pending":
            raise ValueError("invalid historical proposal status")

    if proposal["status"] == "pending":
        receipt = DATA_DIR / "writes" / (proposal["request_id"] + ".json")
        if receipt.parent.is_symlink() or receipt.is_symlink():
            raise ValueError("invalid write outcome path")
        if receipt.exists():
            if not receipt.is_file():
                raise ValueError("invalid write outcome record")
            events.append(dict(kind="unreconciled_attempt", request_id=proposal["request_id"], target=proposal["target"],
                               status="write receipt exists; inspect and reconcile before another action"))

    if completed_batch:
        # Discount only successful writes made earlier in this approved invocation; every other feedback event still changes the checkpoint.
        events = [event for event in events if not (event["kind"] == "write_outcome" and event["status"] == "executed" and
                  event["comment_status"] == "succeeded" and event["state_status"] == "succeeded" and
                  completed_batch.get(event["request_id"]) == event["target"])]

    checkpoint = "v1:" + digest(canonical([FEEDBACK_POLICY, repo, number, events])) if events else None

    return dict(checkpoint=checkpoint, events=events)
