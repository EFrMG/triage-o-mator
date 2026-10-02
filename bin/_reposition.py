"""Optional Reposition bridge; legacy cache commands never import that package."""

import argparse
import os
import sys
from pathlib import Path

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
    options = (value.partition("=")[0] for value in arguments)
    if any(option.startswith("--") and len(option) > 2 and "--cache".startswith(option) for option in options):
        parser.error("the cache namespace is owned by this install; --cache cannot override it")

    # Installs link bin/ to this checkout. Keep the optional engine in one
    # checkout-local environment without requiring shell activation.
    venv = Path(__file__).resolve().parent.parent / ".reposition-venv"
    python = venv / "bin/python"
    if python.is_file() and Path(sys.prefix).resolve() != venv.resolve():
        os.execv(str(python), [str(python), *sys.argv])

    try:
        from reposition import __version__
        from reposition.cli import main
    except ModuleNotFoundError as exc:
        if exc.name != "reposition":
            sys.stderr.write(f"error: Reposition cannot load because Python module {exc.name!r} is unavailable; check the Python runtime or reinstall Reposition.\n")
            return 2

        sys.stderr.write(
            f"error: install Reposition {SUPPORTED_REPOSITION} in this Python environment; see docs/reposition.md. Existing cache search remains available.\n"
        )
        return 2
    except ImportError as exc:
        sys.stderr.write(f"error: Reposition is present but cannot load ({exc}); check the Python runtime or reinstall Reposition.\n")
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
