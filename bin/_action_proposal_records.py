"""Validate saved non-PR-closure action proposals and expose their feedback."""

import json
import re
import uuid

from _evidence import canonical, digest
from _triage import DATA_DIR


def proposal_path(kind, number):
    if kind not in ("issue", "pr") or type(number) is not int or number < 1:
        raise ValueError("invalid action proposal item")

    return DATA_DIR / "action-proposals" / f"{kind}-{number}.json"


def valid_decision_hold(value):
    question = value.get("decision_question")
    if question is not None:
        if not isinstance(question, str) or not question.strip() or len(question) > 2000:
            return False
        review = value.get("decision_review")
        if review is not None and (not isinstance(review, dict) or type(review.get("reviewed")) is not bool or not isinstance(review.get("by"), str) or not isinstance(review.get("at"), str)):
            return False

        answer = value.get("decision_resolution")
        if answer is None:
            return True

        history = value.get("history", [])
        if not isinstance(answer, dict) or not isinstance(history, list):
            return False
        held = next((version for version in reversed(history) if isinstance(version, dict) and version.get("checksum") == answer.get("held_checkpoint")), None)
        return (held is not None and held.get("decision_question") == question and held.get("target") == value.get("target") and
                held.get("action") == value.get("action") and held.get("operation") == value.get("operation") and
                isinstance(answer.get("by"), str) and bool(answer["by"].strip()) and len(answer["by"]) <= 200 and
                isinstance(answer.get("at"), str) and bool(re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", answer["at"])) and
                isinstance(answer.get("reason"), str) and bool(answer["reason"].strip()) and len(answer["reason"]) <= 10000)

    history = value.get("history", [])
    if not isinstance(history, list):
        return False
    held = next((version for version in reversed(history) if isinstance(version, dict) and version.get("decision_question")), None)
    resolution = value.get("decision_resolution")
    if held is None:
        return resolution is None
    if not isinstance(resolution, dict):
        return False

    prior = held.get("decision_review") or {}
    return (resolution.get("held_checkpoint") == held.get("checksum") and
            isinstance(resolution.get("by"), str) and bool(resolution["by"].strip()) and len(resolution["by"]) <= 200 and
            isinstance(resolution.get("at"), str) and bool(re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", resolution["at"])) and
            resolution["at"] != prior.get("at") and
            isinstance(resolution.get("reason"), str) and bool(resolution["reason"].strip()) and len(resolution["reason"]) <= 10000)


def valid_reconsideration(versions):
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
            explanation.get("rejected_checkpoint") == rejected.get("checksum") and
            isinstance(explanation.get("by"), str) and bool(explanation["by"].strip()) and len(explanation["by"]) <= 200 and
            isinstance(explanation.get("reason"), str) and bool(explanation["reason"].strip()) and len(explanation["reason"]) <= 10000 and
            isinstance(explanation.get("at"), str) and bool(re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", explanation["at"])))


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
    if kind == "pr" and value["operation"] == "close":
        raise ValueError("PR closure belongs to auto-close")
    if value.get("target") != f"https://{value.get('host')}/{repo}/{'pull' if kind == 'pr' else 'issues'}/{number}":
        raise ValueError(f"invalid action proposal target: {path.name}")
    if not isinstance(value.get("action"), str) or not value["action"] or not isinstance(value.get("comment"), str) or not value["comment"].strip():
        raise ValueError(f"invalid action proposal content: {path.name}")
    if not valid_decision_hold(value):
        raise ValueError(f"invalid human decision question: {path.name}")

    if not isinstance(value.get("history", []), list):
        raise ValueError("invalid action proposal history")
    versions = [*value.get("history", []), value]
    for index, version in enumerate(versions):
        if not isinstance(version, dict) or any(version.get(field) != value[field] for field in ("repo", "kind", "number")) or version.get("target") != f"https://{version.get('host')}/{repo}/{'pull' if kind == 'pr' else 'issues'}/{number}":
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
                    not isinstance(rejection.get("by"), str) or not rejection["by"].strip() or len(rejection["by"]) > 200 or
                    not isinstance(rejection.get("reason"), str) or len(rejection["reason"]) > 10000 or
                    not isinstance(rejection.get("at"), str) or not re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ", rejection["at"])):
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


def feedback(kind, number, repo):
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
                               state_url=outcome["state_change"].get("url")))

    if record["status"] == "pending":
        receipt = DATA_DIR / "writes" / (record["request_id"] + ".json")
        if receipt.parent.is_symlink() or receipt.is_symlink():
            raise ValueError("invalid action write outcome path")
        if receipt.exists():
            events.append(dict(kind="unreconciled_attempt", request_id=record["request_id"], target=record["target"],
                               status="write receipt exists; inspect and reconcile before another action"))

    checkpoint = "v1:" + digest(canonical(["action-proposal-feedback-v1", repo, kind, number, events])) if events else None
    return dict(checkpoint=checkpoint, events=events)
