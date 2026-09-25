"""Durable, repository-scoped verdicts that two similar items are *not* duplicates of each other, so bin/similar stops offering the pair; no GitHub or ledger mutations.

A verdict is about one pair, not about an item or a cluster: if a third similar item turns up later, it forms pairs of its own, which are offered as usual. That is how a settled comparison stays settled while a new report still gets looked at.
"""

from _triage import DATA_DIR, load_jsonl, now_iso, save_jsonl
from _storage import locked

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
    kind, low, high = pair_key(kind, first, second)
    row = {"kind": kind, "a": low, "b": high, "by": by, "at": now_iso(), "note": note}
    with locked(PATH):
        records = [rec for rec in load() if record_key(rec) != (kind, low, high)]
        save(records + [row])

    return row


def remove(kind, first, second):
    key = pair_key(kind, first, second)
    with locked(PATH):
        records = load()
        kept = [rec for rec in records if record_key(rec) != key]
        if len(kept) == len(records):
            return False

        save(kept)

    return True


class TitleVerdicts:
    """One view of the recorded pairs, so every title consumer excludes the same comparisons."""

    def __init__(self):
        self.records = {record_key(row): row for row in load()}
        self.by_item = {}
        for (kind, a, b), row in self.records.items():
            self.by_item.setdefault((kind, a), {})[(kind, b)] = row
            self.by_item.setdefault((kind, b), {})[(kind, a)] = row

    def candidates(self, index, key, include_checked=False, **options):
        excluded = self.by_item.get(key, {})
        candidates = index.similar(key, excluded=() if include_checked else excluded, **options)
        if include_checked:
            for candidate in candidates:
                row = excluded.get((candidate["kind"], candidate["number"]))
                if row:
                    candidate["negative_verdict"] = row

        return candidates

    def pairs(self, index, min_score, include_checked=False):
        selected = []
        for pair in index.pairs(min_score=min_score):
            row = self.records.get(pair_key(pair["item"]["kind"], pair["item"]["number"], pair["original"]["number"]))
            if row and not include_checked:
                continue

            if row:
                pair["negative_verdict"] = row

            selected.append(pair)

        return selected
