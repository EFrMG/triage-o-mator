"""Offline duplicate-candidate ranking over ledger titles, shared by bin/similar and bin/batch.

Only titles are compared: the ledger has no bodies, and fetching one per item would cost an API call each. Scores are TF-IDF cosine similarity over words and adjacent word pairs, so two titles sharing rare terms ("supergfxd", "T2 Ethernet") score far higher than two sharing common ones ("fix", "Hyprland"). A high score means "read these two side by side", never "these are duplicates".
"""

import math
import re
from collections import Counter, defaultdict

DEFAULT_TOP = 5
DEFAULT_MIN_SCORE = 0.35
# Backlog-wide pair lists use a higher bar than a single item's candidates: they are browsed cold, so weak matches would bury the strong ones.
DEFAULT_PAIR_MIN_SCORE = 0.5

# Words that say nothing about which report an item is. Kept small on purpose: domain words ("hyprland", "waybar") are left to IDF, which already down-weights whatever is common in this particular repo.
STOPWORDS = set(
    """
    a an and are as at be but by can do does doesn't don't for from has have if in into is isn't it its not of on or
    so than that the then there this to too up was when where which while with without after before via vs
    add adds added allow allows fix fixes fixed fixing make makes made update updates use uses using support
    issue bug feature request pr please should would could cannot can't won't new instead also only just now
    """.split()
)

TOKEN_RE = re.compile(r"[a-z0-9][a-z0-9_+.-]*")


def _stem(word):
    for suffix in ("ing", "ed", "es", "s"):
        if len(word) > len(suffix) + 3 and word.endswith(suffix):
            return word[: -len(suffix)]

    return word


def terms(title):
    words = [_stem(w.strip(".-")) for w in TOKEN_RE.findall(title.lower())]
    words = [w for w in words if w and w not in STOPWORDS and len(w) > 1]

    return words + [f"{a} {b}" for a, b in zip(words, words[1:])]


class TitleIndex:
    def __init__(self, records):
        self.records = {(r["kind"], r["number"]): r for r in records}
        counts = {key: Counter(terms(r.get("title", ""))) for key, r in self.records.items()}
        df = Counter(t for c in counts.values() for t in c)
        n = len(counts)
        self.idf = {t: math.log((n + 1) / (d + 1)) + 1 for t, d in df.items()}
        self.vectors = {}
        self.postings = defaultdict(list)
        for key, c in counts.items():
            vec = self._weigh(c)
            self.vectors[key] = vec
            for t, w in vec.items():
                self.postings[t].append((key, w))

    def _weigh(self, counts):
        vec = {t: (1 + math.log(f)) * self.idf.get(t, 1.0) for t, f in counts.items()}
        norm = math.sqrt(sum(w * w for w in vec.values())) or 1.0

        return {t: w / norm for t, w in vec.items()}

    def similar(self, key, top=DEFAULT_TOP, min_score=DEFAULT_MIN_SCORE, any_kind=False):
        """Rank other ledger items by title similarity to `key`, same kind unless any_kind."""
        vec = self.vectors.get(key)
        if vec is None:
            return []

        scores = defaultdict(float)
        for t, w in vec.items():
            for other, ow in self.postings[t]:
                scores[other] += w * ow

        ranked = sorted(
            (
                (s, other)
                for other, s in scores.items()
                if other != key and s >= min_score and (any_kind or other[0] == key[0])
            ),
            key=lambda pair: (-pair[0], pair[1][1]),
        )

        return [candidate(self.records[other], s) for s, other in ranked[:top]]


    def search(self, text, top=20, min_score=0.1, open_only=True):
        """Rank ledger items of either kind by how well their titles match free text ("suspend lid thinkpad"), for gathering a topic into a group."""
        vec = self._weigh(Counter(terms(text)))
        scores = defaultdict(float)
        for t, w in vec.items():
            for other, ow in self.postings.get(t, ()):
                scores[other] += w * ow

        ranked = sorted(
            ((s, key) for key, s in scores.items() if s >= min_score and (not open_only or self.records[key].get("state") == "open")),
            key=lambda pair: (-pair[0], pair[1][1]),
        )

        return [candidate(self.records[key], s) for s, key in ranked[:top]]

    def pairs(self, min_score=DEFAULT_PAIR_MIN_SCORE):
        """Every pair of open, same-kind items scoring at least min_score, best first. In each pair `item` is the newer one (higher number), which is usually the duplicate, and `original` the older."""
        best = {}
        for key, rec in self.records.items():
            if rec.get("state") != "open":
                continue

            for c in self.similar(key, top=10, min_score=min_score):
                if c["state"] != "open":
                    continue

                pair = tuple(sorted([key, (c["kind"], c["number"])], key=lambda k: k[1]))
                best[pair] = max(best.get(pair, 0), c["score"])

        ordered = sorted(best.items(), key=lambda kv: (-kv[1], -kv[0][1][1]))

        return [{"score": score, "item": candidate(self.records[newer], score), "original": candidate(self.records[older], score)} for (older, newer), score in ordered]


def candidate(rec, score):
    return {
        "number": rec["number"],
        "kind": rec["kind"],
        "title": rec.get("title", ""),
        "state": rec.get("state", ""),
        "category": rec.get("category", ""),
        "action": rec.get("action", ""),
        "reviewed": bool(rec.get("reviewed")),
        "score": round(score, 3),
    }
