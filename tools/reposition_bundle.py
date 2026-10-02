#!/usr/bin/env python3
"""Verify or independently rebuild the checked-in Reposition release bundle without network access."""

import argparse
import base64
import csv
import hashlib
import io
import os
import subprocess
import sys
import tarfile
import tempfile
import venv
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "bin"))
from _reposition import BUNDLE, PIP_NAME, PIP_RUN, WHEEL_NAME, _verified_bundle


def verify():
    manifest = _verified_bundle(all_inputs=True)
    retained = {path.name for path in BUNDLE.iterdir() if path.is_file()}
    if retained != set(manifest["files"]) | {"manifest.json"}:
        raise ValueError("Reposition bundle contains unrecorded or missing files")

    expected = f"./{WHEEL_NAME} --hash=sha256:{manifest['files'][WHEEL_NAME]}\n"
    if (BUNDLE / "requirements.txt").read_text(encoding="utf-8") != expected:
        raise ValueError("Reposition installation requirement does not bind the approved wheel")

    with tarfile.open(BUNDLE / "reposition-source.tar.gz") as archive:
        if archive.pax_headers.get("comment") != manifest["source_commit"]:
            raise ValueError("Reposition source archive does not identify the approved commit")

        for member in archive.getmembers():
            path = Path(member.name)
            if not path.parts or path.parts[0] != "reposition-source" or ".." in path.parts or not (member.isfile() or member.isdir()) or member.mtime != manifest["source_date_epoch"]:
                raise ValueError(f"unsafe Reposition source member: {member.name}")

    with zipfile.ZipFile(BUNDLE / WHEEL_NAME) as archive:
        names = archive.namelist()
        if len(names) != len(set(names)) or archive.testzip() is not None:
            raise ValueError("Reposition wheel has duplicate or corrupt members")

        record_name = f"reposition-{manifest['version']}.dist-info/RECORD"
        metadata_name = f"reposition-{manifest['version']}.dist-info/METADATA"
        if record_name not in names or metadata_name not in names:
            raise ValueError("Reposition wheel is missing distribution metadata")

        records = {}
        for name, digest, size in csv.reader(io.StringIO(archive.read(record_name).decode("utf-8"))):
            if name in records:
                raise ValueError(f"duplicate wheel RECORD entry: {name}")

            records[name] = (digest, size)

        if set(records) != set(names):
            raise ValueError("Reposition wheel RECORD does not cover every member")

        for name in names:
            path = Path(name)
            if path.is_absolute() or ".." in path.parts or name.endswith("/"):
                raise ValueError(f"unsafe Reposition wheel member: {name}")

            if name == record_name:
                if records[name] != ("", ""):
                    raise ValueError("wheel RECORD self-entry must have no digest")

                continue

            body = archive.read(name)
            digest = "sha256=" + base64.urlsafe_b64encode(hashlib.sha256(body).digest()).decode("ascii").rstrip("=")
            if records[name] != (digest, str(len(body))):
                raise ValueError(f"Reposition wheel RECORD mismatch: {name}")

        metadata = archive.read(metadata_name).decode("utf-8")
        if f"Name: reposition\n" not in metadata or f"Version: {manifest['version']}\n" not in metadata:
            raise ValueError("Reposition wheel identity differs from its manifest")

        for required in ("LICENSE", "NOTICE", "Vyral-Apache-2.0.txt", "triage-o-mator-MIT.txt"):
            if not any(name.endswith("/" + required) for name in names):
                raise ValueError(f"Reposition wheel lacks license attribution: {required}")

    return manifest


def rebuild(manifest):
    with tempfile.TemporaryDirectory(prefix="reposition-build-") as temporary:
        temporary = Path(temporary)
        venv.create(temporary / "venv", with_pip=False)
        python = temporary / "venv/bin/python"
        environment = dict(os.environ, PIP_NO_INDEX="1", PIP_DISABLE_PIP_VERSION_CHECK="1", SOURCE_DATE_EPOCH=str(manifest["source_date_epoch"]), PYTHONHASHSEED="0")
        pip = [str(python), "-I", "-c", PIP_RUN, str(BUNDLE / PIP_NAME)]
        subprocess.run([*pip, "install", "--no-index", "--no-deps", "--no-cache-dir", str(BUNDLE / "setuptools-84.0.0-py3-none-any.whl")],
                       check=True, capture_output=True, text=True, env=environment)

        with tarfile.open(BUNDLE / "reposition-source.tar.gz") as archive:
            archive.extractall(temporary)

        dist = temporary / "dist"
        subprocess.run([*pip, "wheel", "--no-index", "--no-deps", "--no-build-isolation", "--no-cache-dir", "--wheel-dir", str(dist), str(temporary / "reposition-source")],
                       check=True, capture_output=True, text=True, env=environment)
        built = dist / WHEEL_NAME
        if not built.is_file() or built.read_bytes() != (BUNDLE / WHEEL_NAME).read_bytes():
            raise ValueError("independent offline build differs from the approved Reposition wheel")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("verify", "rebuild"))
    args = parser.parse_args()
    manifest = verify()
    if args.action == "rebuild":
        rebuild(manifest)

    print(f"Reposition {manifest['version']} bundle {args.action} passed")


if __name__ == "__main__":
    main()
