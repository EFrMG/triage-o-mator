"""Versioned optimistic revision for a spreadsheet's original ledger row."""

import hashlib
import json

from _triage import REPO


def review_revision(rec):
    baseline = {key: value for key, value in rec.items() if key != "last_synced_at"}
    payload = json.dumps(["csv-review-v1", REPO, baseline], sort_keys=True, separators=(",", ":"), ensure_ascii=False)

    return "v1:" + hashlib.sha256(payload.encode("utf-8")).hexdigest()
