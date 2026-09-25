"""Shared evidence test fixtures."""

from .base import *

def example():
    identity = repository("owner/repo", database_id=42, node_id="R_example")
    revision = dict(updated_at="2026-09-21T12:00:00Z", base_sha="a" * 40, head_sha="b" * 40)
    payload = '[{"path":"src/example.py","status":"modified"}]\n'
    ref = artifact_ref(payload)
    component = dict(status="complete", fetched_at="2026-09-21T12:01:00Z", source=dict(transport="rest", resource="/repos/owner/repo/pulls/1/files"), revision=revision, expected_count=1, received_count=1, pagination_complete=True, truncated=False, error=None, object=ref)
    manifest = dict(schema_version=1, artifact="evidence-snapshot", repository=identity, started_at="2026-09-21T12:00:00Z", completed_at="2026-09-21T12:02:00Z", requested_components=["files"], items=[dict(identity=dict(kind="pr", number=1, database_id=123, node_id="PR_example"), revision=revision, components=dict(files=component))])

    return manifest, {object_name(ref): payload}


class EvidenceFixture(CheckoutTest):
    def python(self, code, ok=True):
        prelude = f"import sys; sys.path.insert(0, {str(self.root / 'bin')!r}); sys.path.append({str(ROOT / 'tests')!r})\n"
        env = dict(self.env, TRIAGE_ROOT=str(self.root))
        result = subprocess.run([sys.executable, "-c", prelude + code], cwd=self.root, env=env, capture_output=True, text=True, timeout=20)
        if ok:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)

        return result

    def setup_code(self):
        return """
from _cache import EvidenceCache
from _evidence import repository, seal_snapshot
from support import example
cache = EvidenceCache(repository('owner/repo'))
cache.initialize()
manifest, payloads = example()
cache.bind_repository(manifest['repository'])
manifest = seal_snapshot(manifest)
"""


HEAD = "b" * 40


PATCH = "@@ -1 +1,2 @@\n-old\n+new\n+more"


DIFF = "diff --git a/old.py b/new.py\nsimilarity index 50%\nrename from old.py\nrename to new.py\nindex 1111111..2222222 100644\n--- a/old.py\n+++ b/new.py\n" + PATCH + "\n"


