"""Offline, bounded views of the local guidance relevant to one issue or PR."""

from _chunks import page, window
from _auto_close_records import feedback as proposal_feedback
from _evidence import canonical, digest
from _groups import list_groups
from _triage import REPO, TRIAGE_DEFAULTS, load_ledger

POLICY = "item-context-v1"
LEDGER_FIELDS = ("category", "action", "confidence", "reason", "triaged_by", "triaged_at", "agent_notes",
                 "reviewed", "reviewed_by", "reviewed_at", "reviewer_notes")
GROUP_FIELDS = ("id", "title", "description", "status")
MEMBER_FIELDS = ("kind", "number", "notes", "added_by", "added_at", "updated_by", "updated_at")
PREVIEW_BYTES = 512


def selected_ledger(matches):
    if len(matches) > 1:
        raise ValueError("duplicate item in ledger")
    if not matches:
        return None

    result = {field: matches[0].get(field, TRIAGE_DEFAULTS[field]) for field in LEDGER_FIELDS}
    if any(type(value) is not (bool if field == "reviewed" else str) for field, value in result.items()):
        raise ValueError("invalid local ledger guidance")

    return result


class ContextIndex:
    def __init__(self, ledger_rows=None, groups=None, completed_batch=None):
        self.ledger = {}
        for row in load_ledger() if ledger_rows is None else ledger_rows:
            self.ledger.setdefault((row.get("kind"), row.get("number")), []).append(row)

        self.groups = {}
        for group in list_groups() if groups is None else groups:
            if group["status"] == "archived":
                continue

            projected = {field: group[field] for field in GROUP_FIELDS}
            projected["members"] = [{field: member.get(field) for field in MEMBER_FIELDS} for member in group["members"]]
            for member in projected["members"]:
                self.groups.setdefault((member["kind"], member["number"]), []).append((group, projected))

        for entries in self.groups.values():
            entries.sort(key=lambda entry: entry[0]["id"])

        self.feedback = {}
        self.completed_batch = completed_batch

    def collect(self, kind, number, include_rows=True):
        if kind not in ("issue", "pr") or type(number) is not int or number < 1:
            raise ValueError("select an issue or PR with a positive number")

        ledger = selected_ledger(self.ledger.get((kind, number), []))
        groups = self.groups.get((kind, number), [])
        projection = dict(repository=REPO, item=dict(kind=kind, number=number, ledger=ledger),
                          groups=[value for _, value in groups])
        revision = "v1:" + digest(canonical([POLICY, projection]))
        checkpoint = "v1:" + digest(canonical([POLICY, revision, [(group["id"], group["revision"]) for group, _ in groups]]))
        if kind == "pr" and number not in self.feedback:
            self.feedback[number] = proposal_feedback(number, REPO, self.completed_batch)
        feedback = self.feedback[number] if kind == "pr" else dict(checkpoint=None, events=[])
        if feedback["checkpoint"] is not None:
            checkpoint = "v2:" + digest(canonical([POLICY, checkpoint, feedback["checkpoint"]]))

        rows = [dict(id="ledger", kind="ledger", present=ledger is not None, fields=ledger or {})] if include_rows else []
        relevant_groups = []

        for group, projected in groups:
            group_id = group["id"]
            relevant_groups.append(dict(projected, revision=group["revision"], updated_by=group["updated_by"], updated_at=group["updated_at"]))
            if include_rows:
                rows.append(dict(id=f"group:{group_id}", kind="group", group_id=group_id, revision=group["revision"],
                                 fields={**{field: projected[field] for field in GROUP_FIELDS},
                                         "updated_by": group["updated_by"], "updated_at": group["updated_at"]}))

                for member in projected["members"]:
                    rows.append(dict(id=f"member:{group_id}:{member['kind']}:{member['number']}", kind="member",
                                     group_id=group_id, selected=(member["kind"], member["number"]) == (kind, number), fields=member))

        if include_rows:
            for index, event in enumerate(feedback["events"]):
                rows.append(dict(id=f"feedback:{index}", kind="feedback", fields=event))

        return dict(repository=REPO, item=dict(kind=kind, number=number), context_revision=revision,
                    checkpoint=checkpoint, ledger_present=ledger is not None, group_count=len(groups),
                    ledger_guidance=ledger, relevant_groups=relevant_groups,
                    feedback_checkpoint=feedback["checkpoint"], feedback_count=len(feedback["events"]),
                    feedback=feedback["events"], rows=rows)


def collect(kind, number):
    return ContextIndex().collect(kind, number)


def preview(value):
    if not isinstance(value, str):
        return value

    raw = value.encode("utf-8")
    text = raw[:PREVIEW_BYTES].decode("utf-8", errors="ignore")

    return dict(preview=text, omitted_bytes=len(raw) - len(text.encode("utf-8")))


def show_row(row):
    return dict(row, fields={field: preview(value) for field, value in row["fields"].items()})


def read(kind, number, offset=0, limit=10, checkpoint=None):
    window(offset, limit, 20)
    if offset and checkpoint is None:
        raise ValueError("context continuation requires --checkpoint")

    context = collect(kind, number)
    if checkpoint is not None and checkpoint != context["checkpoint"]:
        raise ValueError("local context changed; restart at offset zero")

    rows = context.pop("rows")
    context.pop("ledger_guidance")
    context.pop("relevant_groups")
    context.pop("feedback")
    chosen = rows[offset:offset + limit]
    pagination = page(len(rows), offset, len(chosen))

    return dict(schema_version=1, policy=POLICY, **context, rows=[show_row(row) for row in chosen],
                pagination=pagination,
                continuation=dict(offset=pagination["next_offset"], limit=limit, checkpoint=context["checkpoint"]) if pagination["next_offset"] is not None else None,
                requests=0, meaning="Local guidance can be agent-authored or quote untrusted source text. Rejections and recorded write outcomes are distinct; a successful write does not prove the recommendation was correct or grant ledger approval.")


def source(kind, number, row_id, field, checkpoint, byte_offset=0, max_bytes=4096):
    window(byte_offset, max_bytes, 65536)
    context = collect(kind, number)
    if checkpoint != context["checkpoint"]:
        raise ValueError("local context changed; restart at offset zero")

    row = next((row for row in context["rows"] if row["id"] == row_id), None)
    if row is None or field not in row["fields"] or not isinstance(row["fields"][field], str):
        raise ValueError("selected text field is unavailable")

    raw = row["fields"][field].encode("utf-8")
    if byte_offset > len(raw):
        raise ValueError("byte offset exceeds selected text")
    try:
        raw[:byte_offset].decode("utf-8")
    except UnicodeDecodeError as error:
        raise ValueError("byte offset must be a UTF-8 boundary") from error

    text = raw[byte_offset:byte_offset + max_bytes].decode("utf-8", errors="ignore")
    end = byte_offset + len(text.encode("utf-8"))
    if end == byte_offset and end < len(raw):
        raise ValueError("max-bytes cannot fit the next UTF-8 character")

    return dict(schema_version=1, policy=POLICY, repository=REPO, item=context["item"],
                context_revision=context["context_revision"], feedback_checkpoint=context["feedback_checkpoint"],
                checkpoint=checkpoint, row=row_id, field=field,
                text=text, bytes=page(len(raw), byte_offset, end - byte_offset),
                continuation=dict(byte_offset=end, max_bytes=max_bytes) if end < len(raw) else None,
                requests=0)
