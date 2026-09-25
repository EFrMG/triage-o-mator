"""Explicit compatibility enrichment options for multi-item consumers. Defaults stay legacy."""

from _evidence import DEFAULT_MAX_AGE
from _triage import enrich_item


def add_cache_arguments(parser):
    parser.add_argument("--cache-mode", choices=("offline", "cache-preferred", "refresh"), help="use shared evidence instead of legacy enrichment")
    parser.add_argument("--snapshot", help="fixed snapshot for every selected item; requires --cache-mode offline")
    parser.add_argument("--host", default="github.com", help="explicit host for cache mode only")
    parser.add_argument("--max-age", type=int, default=DEFAULT_MAX_AGE, help="cache freshness window in seconds")
    parser.add_argument("--request-budget", type=int, default=100, help="maximum requests per selected item in cache mode")


def validate_cache_arguments(parser, args, enrich):
    if args.cache_mode and not enrich:
        parser.error("--cache-mode requires --enrich or --diff")

    if args.snapshot and args.cache_mode != "offline":
        parser.error("--snapshot requires --cache-mode offline")

    if args.host != "github.com" and not args.cache_mode:
        parser.error("--host requires --cache-mode")

    if args.max_age < 0 or args.request_budget < 1:
        parser.error("--max-age must be nonnegative and --request-budget positive")


def enrich_selected(rec, args):
    if not args.cache_mode:
        return enrich_item(rec, include_diff=args.diff)

    # Keep the legacy path independent of cache initialization and its dependencies.
    from _reader import enrich_cached
    from _acquire import ReadFailure

    try:
        result = enrich_cached(rec, args.cache_mode, host=args.host, max_age=args.max_age, budget=args.request_budget, include_diff=args.diff, snapshot_id=args.snapshot)
    except ReadFailure as error:
        raise ValueError(str(error)) from error

    stopped = result["evidence"]["stats"].get("stopped")
    if stopped:
        raise ValueError(f"enrichment stopped: {stopped}; cached checkpoints remain; no completed output published")

    return result
