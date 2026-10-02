"""Optional Reposition bridge; legacy cache commands never import that package."""

import argparse
import importlib.util
import os
import subprocess
import sys
import venv
from pathlib import Path

from _install import CODE_ROOT

COMMANDS = {
    "search-index": "cache-index",
    "query": "cache-query",
    "retrieve": "cache-retrieve",
    "search-info": "cache-info",
}
SUPPORTED_REPOSITION = "0.2.0.dev1"
REPOSITION_SOURCE = "git+https://github.com/Univeracity/reposition.git@10bd0641b50d514984dad6dee480f140ab86ee44"
DEFAULT_INDEX_BYTES = 512 * 1024 * 1024
STATE_DIR = CODE_ROOT / ".reposition-state"
DISABLED = STATE_DIR / "disabled"
VENV = CODE_ROOT / ".reposition-venv"


def environment_status():
    if DISABLED.exists():
        if DISABLED.is_symlink():
            return dict(enabled=False, state="off", scope="checkout", fallback=True, reason="Reposition state marker is a symlink")

        try:
            reason = DISABLED.read_text(encoding="utf-8").strip()[:500]
        except OSError:
            reason = "Reposition state marker cannot be read"

        return dict(enabled=False, state="off", scope="checkout", fallback=bool(reason), reason=reason or None)

    python = VENV / "bin/python"
    if not python.is_file() and importlib.util.find_spec("reposition") is None:
        return dict(enabled=False, state="off", scope="checkout", fallback=False, reason=None)

    executable = python if python.is_file() else Path(sys.executable)
    try:
        check = subprocess.run([str(executable), "-c", f"import reposition, sqlite3; assert reposition.__version__ == '{SUPPORTED_REPOSITION}'; sqlite3.connect(':memory:').execute('CREATE VIRTUAL TABLE fts_check USING fts5(text)')"],
                               capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return dict(enabled=False, state="unavailable", scope="checkout", fallback=False, reason=None)

    return dict(enabled=check.returncode == 0, state="on" if check.returncode == 0 else "unavailable", scope="checkout", fallback=False, reason=None)


def set_environment(enabled):
    from _storage import locked

    if STATE_DIR.is_symlink() or VENV.is_symlink() or DISABLED.is_symlink():
        raise ValueError("Reposition environment paths must not be symlinks")

    STATE_DIR.mkdir(exist_ok=True)
    with locked(STATE_DIR / "control"):
        if not enabled:
            DISABLED.write_text("", encoding="utf-8")
            return environment_status()

        DISABLED.write_text("", encoding="utf-8")
        python = VENV / "bin/python"

        def create_environment():
            if importlib.util.find_spec("ensurepip") is None:
                raise ValueError("Python virtualenv setup needs ensurepip and pip; install the python3-venv package")

            try:
                venv.create(VENV, with_pip=True, clear=True)
            except SystemExit as exc:
                raise ValueError("Python virtualenv setup failed; check that ensurepip and python3-venv are available") from exc

        recreate = not python.is_file()
        if not recreate:
            try:
                probe = subprocess.run([str(python), "-c", "import sys, sqlite3"], capture_output=True, text=True, timeout=10)
                recreate = probe.returncode != 0
            except (OSError, subprocess.TimeoutExpired):
                recreate = True

        if recreate:
            create_environment()

        check = subprocess.run([str(python), "-c", f"import reposition; assert reposition.__version__ == '{SUPPORTED_REPOSITION}'"],
                               capture_output=True, text=True, timeout=10)
        if check.returncode != 0:
            pip = subprocess.run([str(python), "-m", "pip", "--version"], capture_output=True, text=True, timeout=10)
            if pip.returncode != 0:
                create_environment()

            try:
                subprocess.run([str(python), "-m", "pip", "install", "--no-deps", "--force-reinstall", REPOSITION_SOURCE], check=True, timeout=120, capture_output=True, text=True)
            except subprocess.CalledProcessError as exc:
                raise ValueError("pinned Reposition installation failed; environment remains OFF") from exc
            except subprocess.TimeoutExpired as exc:
                raise ValueError("pinned Reposition installation timed out; environment remains OFF") from exc

        # Verify the installed package and FTS5 before exposing it to any install.
        DISABLED.unlink()
        status = environment_status()
        if not status["enabled"]:
            DISABLED.write_text("", encoding="utf-8")
            raise ValueError("Reposition installed but its pinned version or SQLite FTS5 is unavailable")

        return status


def ensure_environment(retry=False):
    """Automatic setup stops retrying after a failure until the operator turns automatic download OFF and ON."""
    current = environment_status()
    if current["enabled"] or DISABLED.exists() and not retry:
        return current

    try:
        return set_environment(True)
    except (OSError, ValueError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        try:
            if not STATE_DIR.is_symlink() and not DISABLED.is_symlink():
                STATE_DIR.mkdir(exist_ok=True)
                DISABLED.write_text((str(exc) or "Reposition setup failed")[:500] + "\n", encoding="utf-8")
        except OSError:
            pass

        return dict(enabled=False, state="off", scope="checkout", fallback=True, reason=(str(exc) or "Reposition setup failed")[:500])


def bounded_index_arguments(cache, arguments):
    """Keep managed Reposition views inside the same practical 5 GB budget as evidence, including replacement space."""
    from _cache import DATASET_LIMIT_BYTES, DATASET_RESERVE_BYTES

    parser = argparse.ArgumentParser(add_help=False, allow_abbrev=False)
    parser.add_argument("--db")
    parser.add_argument("--max-index-bytes", type=int, default=DEFAULT_INDEX_BYTES)
    options, _ = parser.parse_known_args(arguments)
    sidecars = cache.root.parent / "reposition"
    if options.db:
        destination = Path(options.db).absolute()
        if destination.is_symlink() or destination.parent.resolve() != sidecars.resolve():
            raise ValueError("search indexes must be stored in the managed reposition directory beside the evidence cache")

    if options.max_index_bytes < 65536:
        raise ValueError("max_index_bytes must be at least 65536")

    available = DATASET_LIMIT_BYTES - cache.usage()["allocated_bytes"] - DATASET_RESERVE_BYTES
    maximum = min(options.max_index_bytes, available)
    if maximum < 65536:
        raise ValueError("combined 5 GB cache storage limit reached; index build stopped with saved evidence intact")

    return [*arguments, "--max-index-bytes", str(maximum)]


def ensure_sidecar_ignore(cache):
    """Protect derived views in installs created before the install-level ignore rule existed."""
    from _storage import atomic_writer, sync_directory

    sidecars = cache.root.parent / "reposition"
    if sidecars.is_symlink():
        raise ValueError("managed reposition directory must not be a symlink")

    if sidecars.exists() and not sidecars.is_dir():
        raise ValueError("managed reposition directory must be a directory")

    sidecars.mkdir(exist_ok=True)
    ignore = sidecars / ".gitignore"
    if ignore.is_symlink():
        raise ValueError("managed reposition .gitignore must not be a symlink")

    if ignore.exists():
        if not ignore.is_file() or ignore.read_text(encoding="utf-8") != "*\n":
            raise ValueError("managed reposition directory has a custom .gitignore; preserve and inspect it before indexing")

        return

    with atomic_writer(ignore) as out:
        out.write("*\n")

    sync_directory(sidecars.parent)


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

    if DISABLED.exists():
        sys.stderr.write("error: Reposition is OFF for this checkout; turn automatic download OFF and ON to retry setup, or run bin/reposition-env enable. Native cache search remains available.\n")
        return 2

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
    python = VENV / "bin/python"
    if python.is_file() and Path(sys.prefix).resolve() != VENV.resolve():
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

    command = COMMANDS[argv[position]]
    if command == "cache-index":
        from _storage import locked

        with locked(cache.path("cache.json")):
            try:
                bounded_index_arguments(cache, arguments)
                ensure_sidecar_ignore(cache)
                bounded = bounded_index_arguments(cache, arguments)
            except ValueError as exc:
                parser.error(str(exc))

            return main([command, "--cache", str(cache.root), *bounded])

    return main([command, "--cache", str(cache.root), *arguments])
