"""Validate saved action proposals and expose their feedback."""

import json
import re
import uuid

from _evidence import canonical, digest
from _triage import DATA_DIR, UTC_TIMESTAMP_RE, item_url


def proposal_path(kind, number):
    if kind not in ("issue", "pr") or type(number) is not int or number < 1:
        raise ValueError("invalid action proposal item")

    return DATA_DIR / "action-proposals" / f"{kind}-{number}.json"


def receipt_path(request_id):
    """Where comment-plus saves the outcome of one write request."""
    return DATA_DIR / "writes" / f"{request_id}.json"


def has_control_characters(text):
    return any((ord(char) < 32 and char not in "\n\t") or 127 <= ord(char) <= 159 for char in text)


def attributed(entry, reason_required=True):
    """A person's saved answer, rejection or reconsideration: who, when and why, within the stored size limits."""
    by, at, reason = entry.get("by"), entry.get("at"), entry.get("reason")
    return (isinstance(by, str) and bool(by.strip()) and len(by) <= 200 and
            isinstance(at, str) and bool(UTC_TIMESTAMP_RE.fullmatch(at)) and
            isinstance(reason, str) and len(reason) <= 10000 and (bool(reason.strip()) or not reason_required))


def current_cycle(history):
    last_completed = next((index for index in range(len(history) - 1, -1, -1) if isinstance(history[index], dict) and history[index].get("status") == "executed"), -1)
    return history[last_completed + 1:]


def valid_decision_hold(value):
    history = value.get("history", [])
    if not isinstance(history, list):
        return False

    history = current_cycle(history)

    question = value.get("decision_question")
    answer = value.get("decision_resolution")
    if question is None or answer is None:
        return answer is None and (question is None or isinstance(question, str) and bool(question.strip()) and len(question) <= 2000)

    if not isinstance(question, str) or not question.strip() or len(question) > 2000 or not isinstance(answer, dict):
        return False

    held = next((version for version in reversed(history) if isinstance(version, dict) and version.get("checksum") == answer.get("held_checkpoint")), None)
    return (held is not None and held.get("decision_question") == question and held.get("target") == value.get("target") and
            held.get("action") == value.get("action") and held.get("operation") == value.get("operation") and attributed(answer))


def valid_reconsideration(versions):
    versions = [*current_cycle(versions[:-1]), versions[-1]]
    current = versions[-1]
    index = next((position for position in range(len(versions) - 2, -1, -1) if versions[position].get("status") == "rejected"), None)
    explanation = current.get("reconsideration")
    if index is None:
        return explanation is None
    if not isinstance(explanation, dict):
        return False

    rejected = versions[index]
    previous = versions[index - 1] if index else None
    return (isinstance(previous, dict) and previous.get("status") == "pending" and
            rejected.get("rejection", {}).get("proposal_checkpoint") == previous.get("checksum") and
            explanation.get("rejected_checkpoint") == rejected.get("checksum") and attributed(explanation))


def load(path, repo):
    if path.parent.is_symlink() or path.is_symlink() or not path.is_file():
        raise ValueError(f"unavailable action proposal: {path.name}")

    value = json.loads(path.read_text(encoding="utf-8"))
    checksum = value.pop("checksum", None)
    if not isinstance(checksum, str) or digest(canonical(value)) != checksum:
        raise ValueError(f"action proposal checksum changed: {path.name}")

    value["checksum"] = checksum
    kind, number = value.get("kind"), value.get("number")
    if (value.get("artifact") != "action-proposal" or value.get("schema_version") != 1 or value.get("repo") != repo or
            kind not in ("issue", "pr") or type(number) is not int or number < 1 or path.name != f"{kind}-{number}.json" or
            value.get("operation") not in ("comment", "close", "reopen") or value.get("status") not in ("pending", "rejected", "executed", "uncertain")):
        raise ValueError(f"invalid action proposal identity or status: {path.name}")
    if value.get("target") != item_url(value.get("host"), repo, kind, number):
        raise ValueError(f"invalid action proposal target: {path.name}")
    action = value.get("action")
    if (action is None and (kind != "pr" or value["operation"] != "close") or
            action is not None and (not isinstance(action, str) or not action) or
            not isinstance(value.get("comment"), str) or not value["comment"].strip()):
        raise ValueError(f"invalid action proposal content: {path.name}")
    if not valid_decision_hold(value):
        raise ValueError(f"invalid human decision question: {path.name}")

    if not isinstance(value.get("history", []), list):
        raise ValueError("invalid action proposal history")
    versions = [*value.get("history", []), value]
    for index, version in enumerate(versions):
        if not isinstance(version, dict) or any(version.get(field) != value[field] for field in ("repo", "kind", "number")) or version.get("target") != item_url(version.get("host"), repo, kind, number):
            raise ValueError("invalid historical action proposal identity")
        try:
            if str(uuid.UUID(version["request_id"])) != version["request_id"]:
                raise ValueError("noncanonical action request ID")
        except (ValueError, KeyError, TypeError) as error:
            raise ValueError("invalid historical action request ID") from error
        if version.get("status") == "rejected":
            rejection = version.get("rejection")
            previous = versions[index - 1] if index else None
            if (not isinstance(rejection, dict) or not isinstance(previous, dict) or previous.get("status") != "pending" or
                    rejection.get("proposal_checkpoint") != previous.get("checksum") or not isinstance(rejection.get("proposal_checkpoint"), str) or
                    not re.fullmatch(r"[0-9a-f]{64}", rejection["proposal_checkpoint"]) or version.get("request_id") != previous.get("request_id") or
                    not attributed(rejection, reason_required=False)):
                raise ValueError("invalid action proposal rejection")
        elif version.get("rejection") is not None:
            raise ValueError("rejection on non-rejected action proposal")
        elif version.get("status") in ("executed", "uncertain"):
            outcome = version.get("outcome")
            if not isinstance(outcome, dict) or outcome.get("request_id") != version["request_id"] or not isinstance(outcome.get("comment"), dict) or not isinstance(outcome.get("state_change"), dict):
                raise ValueError("invalid action proposal outcome")
        elif version.get("status") != "pending":
            raise ValueError("invalid historical action proposal status")

    if not valid_reconsideration(versions):
        raise ValueError("invalid action proposal reconsideration")

    return value


def direct_execution_eligible(value):
    """A saved proposal or its listed row may run without a person only with complete evidence for its own item, no declared gap, no reconsidered rejection and no open question."""
    inputs = value["inputs"]
    complete_target = any(selected["kind"] == value["kind"] and selected["number"] == value["number"] and
                          all(component["status"] in ("complete", "not_applicable") for component in selected["components"].values())
                          for selected in inputs["evidence"])

    return complete_target and not inputs["evidence_gaps"] and not value.get("reconsideration") and not value.get("decision_question")


def feedback(kind, number, repo, completed_batch=None):
    path = proposal_path(kind, number)
    if not path.exists() and not path.is_symlink():
        return dict(checkpoint=None, events=[])

    record = load(path, repo)
    events = []
    for version in [*record.get("history", []), record]:
        if version["status"] == "rejected":
            rejection = version["rejection"]
            events.append(dict(kind="rejection", by=rejection["by"], at=rejection["at"], reason=rejection["reason"],
                               proposal_checkpoint=rejection["proposal_checkpoint"], request_id=version["request_id"], target=version["target"]))
        elif version["status"] in ("executed", "uncertain"):
            outcome = version["outcome"]
            events.append(dict(kind="write_outcome", status=version["status"], request_id=version["request_id"],
                               target=version["target"], comment_status=outcome["comment"]["status"],
                               state_status=outcome["state_change"]["status"], comment_url=outcome["comment"].get("url"),
                               state_url=outcome["state_change"].get("url"), comment_error=outcome["comment"].get("error"),
                               state_error=outcome["state_change"].get("error")))

    if record["status"] == "pending":
        receipt = receipt_path(record["request_id"])
        if receipt.parent.is_symlink() or receipt.is_symlink():
            raise ValueError("invalid action write outcome path")
        if receipt.exists():
            events.append(dict(kind="unreconciled_attempt", request_id=record["request_id"], target=record["target"],
                               status="write receipt exists; inspect and reconcile before another action"))

    if completed_batch:
        events = [event for event in events if not (event["kind"] == "write_outcome" and event["status"] == "executed" and
                  event["comment_status"] == "succeeded" and event["state_status"] in ("succeeded", "not_requested") and
                  completed_batch.get(event["request_id"]) == event["target"])]

    checkpoint = "v1:" + digest(canonical(["action-proposal-feedback-v1", repo, kind, number, events])) if events else None
    return dict(checkpoint=checkpoint, events=events)
