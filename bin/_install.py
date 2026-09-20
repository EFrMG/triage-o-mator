"""Locating and recording a triage-o-mator install. Not a CLI entry point.

An install is the `triage-o-mator/` directory bin/install-to creates inside the repository being triaged: it holds that repo's config, ledger, groups and reports, and is wired to a triage-o-mator checkout through symlinked bin/, themes/ and prompts/. Every script runs against exactly one install.

Unlike bin/_triage.py, importing this module never requires an install to exist, so bin/install-to can use it to create one.
"""

import json
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

# The directory bin/install-to creates inside a repository being triaged.
INSTALL_DIR_NAME = "triage-o-mator"

# What marks a directory as an install. It records the checkout this install is wired to, which differs per machine and per contributor, so it is deliberately not tracked in the target repo: whoever clones that repo runs bin/install-to once to write their own.
MARKER_NAME = ".triage-install.json"

# owner/repo as GitHub allows it; also what keeps data/<owner>/<repo>/ from ever resolving outside data/.
REPO_RE = re.compile(r"^(?!\.\.?/)[A-Za-z0-9_.-]+/(?!\.\.?$)[A-Za-z0-9_.-]+$")

# The triage-o-mator checkout this script belongs to. Path.resolve follows the bin/ symlink an install is wired through, which is what we want here: this is where the program and the defaults bin/install-to copies live, never where a repo's data goes.
CODE_ROOT = Path(__file__).resolve().parent.parent


def now_iso():
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def work_root():
    """The install this invocation belongs to.

    os.path.abspath deliberately does not follow symlinks: a script run through an install's symlinked bin/ resolves to that install, while Path.resolve would land back in the checkout and send every ledger, batch and report there. TRIAGE_ROOT overrides it, for scripting and tests.
    """
    override = os.environ.get("TRIAGE_ROOT")
    if override:
        return Path(override).expanduser().absolute()

    return Path(os.path.abspath(__file__)).parent.parent


WORK_ROOT = work_root()


def marker_path(root):
    return Path(root) / MARKER_NAME


def read_marker(root):
    """The install record at root, or None if root is not an install (or its marker is unreadable)."""
    try:
        marker = json.loads(marker_path(root).read_text())
    except (OSError, ValueError):
        return None

    return marker if isinstance(marker, dict) else None


def is_install(root):
    return read_marker(root) is not None


def find_install(start=None):
    """The install a directory belongs to: itself, its triage-o-mator/ child, or the nearest of either walking up. None when there is none.

    The TUI and the scripts find their install from the path they were run through; this walk is for callers that only have a working directory, such as a shell sitting in the target repo.
    """
    directory = Path(start or Path.cwd()).expanduser().absolute()
    for candidate in [directory, *directory.parents]:
        if is_install(candidate):
            return candidate

        if is_install(candidate / INSTALL_DIR_NAME):
            return candidate / INSTALL_DIR_NAME

    return None


def require_install(root=WORK_ROOT):
    """Exit unless root is an install. Every bin/ script runs against one, so this is what a checkout run directly, or an install whose marker was lost, hits first."""
    if is_install(root):
        return Path(root)

    sys.exit(f"error: {root} is not a triage-o-mator install.\nRun bin/install-to /path/to/your/repository from the triage-o-mator checkout, then work from the triage-o-mator/ directory it creates there (see docs/install.md).")
