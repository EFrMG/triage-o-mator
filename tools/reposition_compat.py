#!/usr/bin/env python3
"""Run the retained Reposition compatibility trial against an offline disposable checkout."""

import json
import shutil
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

from reposition_bundle import verify

ROOT = Path(__file__).resolve().parents[1]
BUNDLE = ROOT / "vendor/reposition/0.2.0.dev1"


def main():
    verify()

    with tempfile.TemporaryDirectory(prefix="reposition-compat-") as temporary:
        temporary = Path(temporary)
        checkout = temporary / "checkout"
        checkout.mkdir()
        shutil.copytree(ROOT / "bin", checkout / "bin", ignore=shutil.ignore_patterns("__pycache__", "triage-o-mator"))
        shutil.copytree(ROOT / "vendor", checkout / "vendor")
        setup = "import sys; sys.path.insert(0, sys.argv[1]); from _reposition import set_environment; assert set_environment(True)['enabled']"
        subprocess.run([sys.executable, "-I", "-c", setup, str(checkout / "bin")], check=True, capture_output=True, text=True)

        with tarfile.open(BUNDLE / "reposition-source.tar.gz") as archive:
            archive.extractall(temporary)

        script = temporary / "reposition-source/scripts/check_triage_compat.py"
        original = 'if "Reposition 0.2" not in missing.stderr:'
        adapted = 'if "cache index unavailable" not in missing.stderr:'
        source = script.read_text(encoding="utf-8")
        if source.count(original) != 1:
            raise ValueError("retained compatibility trial changed its pre-index expectation")

        # The bundled engine is present before this trial, so the pre-index query must report a missing view rather than a missing package.
        script.write_text(source.replace(original, adapted), encoding="utf-8")
        result = subprocess.run([str(checkout / ".reposition-venv/bin/python"), str(script), "--checkout", str(checkout)], check=True, capture_output=True, text=True)
        receipt = json.loads(result.stdout)
        receipt["pre_index_query_reports_unavailable_view"] = receipt.pop("missing_optional_package_actionable")
        receipt["compatibility_script_adaptation"] = "pre-index error expectation only"
        print(json.dumps(receipt, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
