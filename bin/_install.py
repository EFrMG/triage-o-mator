"""Locating and recording a triage-o-mator install. Not a CLI entry point.

An install is the `triage-o-mator/` directory bin/install-to creates inside the repository being triaged: it holds that repo's config, ledger, groups and reports, and is wired to a triage-o-mator checkout through symlinked bin/, themes/ and prompts/. Every script runs against exactly one install.

Unlike bin/_triage.py, importing this module never requires an install to exist, so bin/install-to can use it to create one.
"""

import json
import os
import re
import subprocess
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


def read_github_labels(repo):
    """Read all label definitions for the selected repository without changing GitHub state."""
    endpoint = f"repos/{repo}/labels?per_page=100"
    command = ["gh", "api", "--method", "GET", "--paginate", endpoint, "--jq", ".[]"]
    try:
        result = subprocess.run(command, capture_output=True, text=True, timeout=60)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ValueError(f"could not read GitHub labels: {error}") from error

    if result.returncode:
        raise ValueError(f"could not read GitHub labels: {result.stderr.strip() or 'gh api failed'}")

    labels = []
    seen_ids = set()
    seen_names = set()
    try:
        for line in result.stdout.splitlines():
            row = json.loads(line)
            label_id, name = row["id"], row["name"]
            if type(label_id) is not int or label_id <= 0 or not isinstance(name, str) or not name:
                raise ValueError("invalid label identity")
            if label_id in seen_ids or name.casefold() in seen_names:
                raise ValueError("duplicate label identity")

            color, description = row.get("color"), row.get("description")
            if not isinstance(color, str) or not re.fullmatch(r"[0-9a-fA-F]{6}", color) or description is not None and not isinstance(description, str):
                raise ValueError("invalid label definition")

            labels.append({"id": label_id, "name": name, "color": color.lower(), "description": description or ""})
            seen_ids.add(label_id)
            seen_names.add(name.casefold())
    except (KeyError, TypeError, json.JSONDecodeError, ValueError) as error:
        raise ValueError(f"invalid GitHub label response: {error}") from error

    return sorted(labels, key=lambda label: (label["name"].casefold(), label["id"]))


def reconcile_label_catalog(taxonomy, repo, labels, observed_at):
    """Keep local guidance and historical names by GitHub label ID, including removed labels."""
    pending_label_catalog(taxonomy, repo)
    catalog = taxonomy.get("label_catalog")

    previous = {row["id"]: row for row in catalog.get("labels", []) + catalog.get("retired", []) if isinstance(row, dict) and type(row.get("id")) is int}
    active = []
    for label in labels:
        old = previous.get(label["id"], {})
        names = list(old.get("previous_names", []))
        if old.get("name") and old["name"] != label["name"] and old["name"] not in names:
            names.append(old["name"])

        entry = {**label, "guidance": old.get("guidance", "")}
        if names:
            entry["previous_names"] = names

        active.append(entry)

    current_ids = {row["id"] for row in labels}
    retired = []
    for label_id, old in previous.items():
        if label_id not in current_ids:
            retired.append({**old, "retired_at": old.get("retired_at") or observed_at})

    taxonomy["label_catalog"] = {"repository": repo, "status": "observed", "observed_at": observed_at, "labels": active, "retired": sorted(retired, key=lambda label: (label["name"].casefold(), label["id"]))}

    return taxonomy


def pending_label_catalog(taxonomy, repo):
    """Never use another repository's label definitions as the selected repository's vocabulary."""
    catalog = taxonomy.get("label_catalog")
    if not isinstance(catalog, dict) or catalog.get("repository") != repo:
        archive = taxonomy.get("label_catalog_archive", {})
        if not isinstance(archive, dict):
            raise ValueError("label_catalog_archive must be a JSON object")

        if isinstance(catalog, dict) and isinstance(catalog.get("repository"), str):
            archive[catalog["repository"]] = catalog

        taxonomy["label_catalog"] = archive.pop(repo, {"repository": repo, "status": "pending", "observed_at": None, "labels": [], "retired": []})
        if archive:
            taxonomy["label_catalog_archive"] = archive
        else:
            taxonomy.pop("label_catalog_archive", None)

    return taxonomy


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
