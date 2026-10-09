#!/usr/bin/env python3
"""Check that a test remote still matches its verified archive before deletion."""

import json
from pathlib import Path
import subprocess
import sys
import tarfile


def get_json(path):
    result = subprocess.run(["gh", "api", "--method", "GET", path], text=True, capture_output=True, check=True)
    return json.loads(result.stdout)


def live_projection(repo, kind):
    rows = []
    page = 1

    while True:
        batch = get_json(f"repos/{repo}/{kind}?state=all&per_page=100&page={page}")

        if not isinstance(batch, list):
            raise RuntimeError(f"Unexpected {kind} list reply")

        rows.extend((item["number"], item["state"], item["updated_at"]) for item in batch)

        if len(batch) < 100:
            return sorted(rows)

        page += 1


def main():
    if len(sys.argv) != 3:
        raise SystemExit("Usage: verify_live_archive.py ARCHIVE.tar.gz OWNER/REPO")

    archive_path = Path(sys.argv[1])
    repo = sys.argv[2]

    with tarfile.open(archive_path, "r:gz") as archive:
        def archived_json(name):
            return json.load(archive.extractfile(name))

        manifest = archived_json("./manifest.json")

        if manifest["repository"] != repo:
            raise RuntimeError("Archive repository differs from deletion target")

        saved_repo = archived_json("./api/repository-after.json")
        current_repo = get_json(f"repos/{repo}")

        for field in ("full_name", "private", "description", "default_branch", "archived"):
            if current_repo.get(field) != saved_repo.get(field):
                raise RuntimeError(f"Live repository {field} differs from archive")

        for kind in ("issues", "pulls"):
            rows = []
            prefix = f"./api/{kind}-after/"

            members = (member for member in archive.getmembers() if member.name.startswith(prefix) and member.name.endswith(".json"))

            for member in sorted(members, key=lambda item: item.name):
                rows.extend((item["number"], item["state"], item["updated_at"]) for item in json.load(archive.extractfile(member)))

            if sorted(rows) != live_projection(repo, kind):
                raise RuntimeError(f"Live {kind} changed after archive capture")

        saved_refs = archive.extractfile("./refs-after.txt").read()

    current_refs = subprocess.run(["git", "ls-remote", "--refs", f"https://github.com/{repo}.git"], capture_output=True, check=True).stdout

    if current_refs != saved_refs:
        raise RuntimeError("Live Git refs changed after archive capture")

    print(f"Live repository still matches archived item revisions and Git refs: {repo}")


if __name__ == "__main__":
    main()
