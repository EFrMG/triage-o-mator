"""Optional Reposition bridge; legacy cache commands never import that package."""

import argparse
import sys

COMMANDS = {
    "search-index": "cache-index",
    "query": "cache-query",
    "retrieve": "cache-retrieve",
    "search-info": "cache-info",
}
SUPPORTED_REPOSITION = "0.2.0.dev1"


def register_commands(commands):
    for name in COMMANDS:
        commands.add_parser(
            name, help="optional offline Reposition retrieval; command --help lists bounded options"
        )


def maybe_run(argv):
    position = 0
    while position < len(argv):
        argument = argv[position]
        if argument in ("--host", "--expected-repo"):
            position += 2
        elif argument.startswith(("--host=", "--expected-repo=")):
            position += 1
        else:
            break
    if position >= len(argv) or argv[position] not in COMMANDS:
        return None

    parser = argparse.ArgumentParser(prog="bin/cache", add_help=False)
    parser.add_argument("--host", default="github.com")
    parser.add_argument("--expected-repo")
    common = parser.parse_args(argv[:position])
    arguments = argv[position + 1 :]
    if any(value == "--cache" or value.startswith("--cache=") for value in arguments):
        parser.error("the cache namespace is owned by this install; --cache cannot override it")

    try:
        from reposition import __version__
        from reposition.cli import main
    except ImportError:
        sys.stderr.write(
            f"error: install Reposition {SUPPORTED_REPOSITION} in this Python environment; see docs/reposition.md. Existing cache search remains available.\n"
        )
        return 2

    if __version__ != SUPPORTED_REPOSITION:
        sys.stderr.write(
            f"error: this bridge supports Reposition {SUPPORTED_REPOSITION}; installed version is {__version__}. Validate compatibility before upgrading. Existing cache search remains available.\n"
        )
        return 2

    if "--help" in arguments or "-h" in arguments:
        return main([COMMANDS[argv[position]], "--cache", "unused", *arguments])

    from _cache import EvidenceCache
    from _evidence import repository
    from _triage import REPO

    if not REPO or common.expected_repo is not None and common.expected_repo != REPO:
        parser.error("install repository changed or is unavailable; reopen the selected dataset")
    cache = EvidenceCache(repository(REPO, host=common.host))
    metadata = cache.require_ready(bound=True)
    if metadata["repository"]["host"] != common.host or metadata["repository"]["full_name"] != REPO:
        parser.error("cache identity differs from the install namespace")

    return main([COMMANDS[argv[position]], "--cache", str(cache.root), *arguments])
