"""Durable, repository-scoped verdicts that two similar items are *not* duplicates of each other, so bin/similar --pairs stops offering the pair; no GitHub or ledger mutations.

A verdict is about one pair, not about an item or a cluster: if a third similar item turns up later, it forms pairs of its own, which are offered as usual. That is how a settled comparison stays settled while a new report still gets looked at.
"""

from _triage import DATA_DIR, load_jsonl, now_iso, save_jsonl

PATH = DATA_DIR / "not-duplicates.jsonl"
FIELDS = ("kind", "a", "b", "by", "at", "note")


def pair_key(kind, first, second):
    """A pair's identity: its kind and its two numbers, smaller first, so which one was compared against which doesn't matter."""
    low, high = sorted((int(first), int(second)))

    return (kind, low, high)


def record_key(rec):
    return pair_key(rec.get("kind", ""), rec.get("a", 0), rec.get("b", 0))


def load():
    return load_jsonl(PATH)


def checked():
    """Every recorded pair, as pair_key tuples, for filtering candidate pairs."""
    return {record_key(rec) for rec in load()}


def save(records):
    save_jsonl(PATH, sorted(records, key=record_key), field_order=FIELDS)


def record(kind, first, second, by, note=""):
    """Record (or re-record) one pair as checked and not duplicates, and return the row."""
    kind, low, high = pair_key(kind, first, second)
    row = {"kind": kind, "a": low, "b": high, "by": by, "at": now_iso(), "note": note}
    save([rec for rec in load() if record_key(rec) != (kind, low, high)] + [row])

    return row


def remove(kind, first, second):
    """Take a verdict back, so the pair is offered again. Returns whether there was one."""
    key = pair_key(kind, first, second)
    records = load()
    kept = [rec for rec in records if record_key(rec) != key]
    if len(kept) == len(records):
        return False

    save(kept)

    return True
