"""Optional Reposition bridge; ordinary cache commands never import that package."""

import argparse
import hashlib
import json
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
BUNDLE = CODE_ROOT / "vendor/reposition/0.2.0.dev1"
APPROVED_MANIFEST_SHA256 = "8c7d8ed1fc06e03adc4c5ad02d9d4db300b17d6372410a1e51e9d997cc2778c2"
WHEEL_NAME = "reposition-0.2.0.dev1-py3-none-any.whl"
PIP_NAME = "pip-25.3-py3-none-any.whl"
DEFAULT_INDEX_BYTES = 512 * 1024 * 1024
STATE_DIR = CODE_ROOT / ".reposition-state"
DISABLED = STATE_DIR / "disabled"
VENV = CODE_ROOT / ".reposition-venv"


def _digest(path):
    if path.is_symlink() or not path.is_file():
        raise ValueError(f"Reposition bundle file is missing or linked: {path.name}")

    checksum = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            checksum.update(block)

    return checksum.hexdigest()


def _verified_bundle(all_inputs=False):
    if BUNDLE.is_symlink() or not BUNDLE.is_dir():
        raise ValueError("audited Reposition bundle is unavailable")

    manifest_path = BUNDLE / "manifest.json"
    if _digest(manifest_path) != APPROVED_MANIFEST_SHA256:
        raise ValueError("audited Reposition manifest checksum differs from the approved build")

    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if manifest.get("schema_version") != 1 or manifest.get("version") != SUPPORTED_REPOSITION:
        raise ValueError("audited Reposition manifest has an unsupported version")

    expected = manifest["files"]
    names = expected if all_inputs else (WHEEL_NAME,)
    for name in names:
        if name != Path(name).name or _digest(BUNDLE / name) != expected[name]:
            raise ValueError(f"audited Reposition bundle checksum differs: {name}")

    return manifest


INSTALLED_CHECK = """
import hashlib, importlib.metadata, pathlib, sqlite3, sys, zipfile
wheel = pathlib.Path(sys.argv[1])
expected = sys.argv[2]
def require(condition):
    if not condition:
        raise ValueError('installed Reposition differs from approved wheel')
try:
    require(hashlib.sha256(wheel.read_bytes()).hexdigest() == expected)
    dist = importlib.metadata.distribution('reposition')
    require(dist.version == '0.2.0.dev1')
    with zipfile.ZipFile(wheel) as archive:
        names = set(archive.namelist())
        for name in names:
            if name.endswith('/') or name.endswith('/RECORD'):
                continue
            installed = pathlib.Path(dist.locate_file(name))
            require(installed.is_file() and not installed.is_symlink())
            require(hashlib.sha256(installed.read_bytes()).digest() == hashlib.sha256(archive.read(name)).digest())
    package = pathlib.Path(dist.locate_file('reposition'))
    require(package.is_dir() and not package.is_symlink())
    for path in package.rglob('*'):
        if path.is_file() and path.suffix != '.pyc' and '__pycache__' not in path.parts:
            require(path.relative_to(package.parent).as_posix() in names)
    import reposition
    require(reposition.__version__ == dist.version)
    require(pathlib.Path(reposition.__file__).resolve() == (package / '__init__.py').resolve())
    sqlite3.connect(':memory:').execute('CREATE VIRTUAL TABLE fts_check USING fts5(text)')
except Exception:
    sys.exit(1)
"""
PIP_RUN = "import runpy, sys; sys.path.insert(0, sys.argv.pop(1)); runpy.run_module('pip', run_name='__main__')"


def _installed_check(python, manifest):
    try:
        check = subprocess.run([str(python), "-I", "-c", INSTALLED_CHECK, str(BUNDLE / WHEEL_NAME), manifest["files"][WHEEL_NAME]],
                               capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return False

    return check.returncode == 0


def environment_status():
    if DISABLED.is_symlink():
        return dict(enabled=False, state="off", scope="checkout", fallback=True, reason="Reposition state marker is a symlink")

    if DISABLED.exists():

        try:
            reason = DISABLED.read_text(encoding="utf-8").strip()[:500]
        except OSError:
            reason = "Reposition state marker cannot be read"

        return dict(enabled=False, state="off", scope="checkout", fallback=bool(reason), reason=reason or None)

    python = VENV / "bin/python"
    if not python.is_file():
        return dict(enabled=False, state="off", scope="checkout", fallback=False, reason=None)

    try:
        manifest = _verified_bundle(all_inputs=True)
    except (OSError, ValueError, KeyError, TypeError):
        return dict(enabled=False, state="unavailable", scope="checkout", fallback=False, reason=None)

    enabled = _installed_check(python, manifest)
    return dict(enabled=enabled, state="on" if enabled else "unavailable", scope="checkout", fallback=False, reason=None)


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
        manifest = _verified_bundle(all_inputs=True)
        python = VENV / "bin/python"

        def create_environment():
            try:
                venv.create(VENV, with_pip=False, clear=True)
            except SystemExit as exc:
                raise ValueError("Python virtualenv setup failed; check that python3-venv is available") from exc

        recreate = not python.is_file()
        if not recreate:
            try:
                probe = subprocess.run([str(python), "-I", "-c", "import sys, sqlite3"], capture_output=True, text=True, timeout=10)
                recreate = probe.returncode != 0
            except (OSError, subprocess.TimeoutExpired):
                recreate = True

        if recreate:
            create_environment()

        if not _installed_check(python, manifest):
            environment = dict(os.environ, PIP_NO_INDEX="1", PIP_DISABLE_PIP_VERSION_CHECK="1")
            try:
                subprocess.run([str(python), "-I", "-c", PIP_RUN, str(BUNDLE / PIP_NAME), "install", "--no-index", "--no-deps", "--force-reinstall", "--require-hashes", "--no-cache-dir", "-r", str(BUNDLE / "requirements.txt")],
                               check=True, timeout=120, capture_output=True, text=True, cwd=BUNDLE, env=environment)
            except subprocess.CalledProcessError as exc:
                raise ValueError("audited Reposition wheel installation failed; environment remains OFF") from exc
            except subprocess.TimeoutExpired as exc:
                raise ValueError("audited Reposition wheel installation timed out; environment remains OFF") from exc

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

    if not environment_status()["enabled"]:
        sys.stderr.write(f"error: audited Reposition is unavailable for this checkout; run bin/reposition-env enable to install Reposition {SUPPORTED_REPOSITION} from the approved wheel. Native cache search remains available.\n")
        return 2

    # Installs link bin/ to this checkout. Keep the optional engine in one
    # checkout-local environment without requiring shell activation.
    python = VENV / "bin/python"
    if python.is_file() and (Path(sys.prefix).resolve() != VENV.resolve() or not sys.flags.isolated):
        os.execv(str(python), [str(python), "-I", *sys.argv])

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
