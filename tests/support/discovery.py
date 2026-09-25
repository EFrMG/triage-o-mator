"""Shared discovery test fixtures."""

from .base import *

class InventoryFixture(AcquisitionFixture):
    def row(self, kind="pr", number=1):
        row = summary(kind, number=number, id=1000 + number, node_id=f"LIST_{number}")
        row.update(html_url=f"https://github.com/owner/repo/{'pull' if kind == 'pr' else 'issues'}/{number}", user=dict(login="author"), labels=[], created_at=row["updated_at"])
        if kind == "pr":
            row["pull_request"] = dict(url=f"https://api.github.com/repos/owner/repo/pulls/{number}")

        return row

    def fetch_inventory(self, rows=None, *extra, ok=True):
        endpoint = "repos/owner/repo/issues?state=open&per_page=100&page=1"
        self.responses[endpoint] = dict(data=rows if rows is not None else [self.row()])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        return self.cli("fetch", "--cache-inventory", *extra, ok=ok)

    def cli(self, command, *args, ok=True):
        result = subprocess.run([str(self.root / "bin" / command), *args], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode == 0, ok, result.stderr + result.stdout)
        return result


class CorpusFixture(AcquisitionFixture):
    row = InventoryFixture.row

    cli = InventoryFixture.cli

    fetch_inventory = InventoryFixture.fetch_inventory

    def create(self, rows=None, scope="open-prs", profile="discussion"):
        self.fetch_inventory(rows or [self.row("pr", 1), self.row("pr", 2)])
        snapshot = json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]
        result = json.loads(self.cli("cache", "corpus-create", "--snapshot", snapshot, "--scope", scope, "--profile", profile).stdout)
        return result["corpus_id"], snapshot

    def seed_details(self):
        for number in (1, 2):
            self.responses[f"repos/owner/repo/pulls/{number}"] = dict(data=summary(number=number, id=100 + number, node_id=f"PR_{number}", html_url=f"https://github.com/owner/repo/pull/{number}", comments=0))
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"] = dict(data=[])

        (self.mock / "responses.json").write_text(json.dumps(self.responses))

    def run_corpus(self, identifier, budget=100):
        self.seed_details()
        return json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", str(budget)).stdout)

    def status(self, identifier):
        return json.loads(self.cli("cache", "corpus-status", identifier).stdout)

    def listing(self, identifier, *args):
        return json.loads(self.cli("cache", "corpus-list", identifier, *args).stdout)


class ChunkFixture(AcquisitionFixture):
    row = InventoryFixture.row

    cli = InventoryFixture.cli

    fetch_inventory = InventoryFixture.fetch_inventory

    def seed(self, count=3, body="private source text 🐈" * 50):
        rows = [dict(self.row("pr", number), body=body) for number in range(1, count + 1)]
        self.fetch_inventory(rows)
        return json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]

    def chunk(self, snapshot, *args, ok=True):
        return self.cli("cache", "chunk", "--snapshot", snapshot, "--kind", "pr", "--number", "1", "--component", "summary", *args, ok=ok)


class CandidateFixture(AcquisitionFixture):
    cli = ChunkFixture.cli

    row = CorpusFixture.row

    fetch_inventory = CorpusFixture.fetch_inventory

    create = CorpusFixture.create

    seed_details = CorpusFixture.seed_details

    run_corpus = CorpusFixture.run_corpus

    def publish(self, specs):
        now = datetime.now(timezone.utc).replace(microsecond=0).isoformat()
        repo = dict(host="github.com", full_name="owner/repo", database_id=42, node_id="R_repo")
        rows, payloads = [], {}
        for number, spec in enumerate(specs, 1):
            identity = dict(kind=spec.get("kind", "pr"), number=number, database_id=100 + number, node_id=f"PR_{number}")
            revision = dict(updated_at=now, base_sha=spec.get("base", "a" * 40), head_sha="b" * 40)
            summary = dict(number=number, id=100 + number, node_id=f"PR_{number}", state=spec.get("state", "open"),
                           title=spec.get("title", f"unrelated{number}"), body="untrusted body text", updated_at=now,
                           base=dict(ref=spec.get("branch", "main"), sha=revision["base_sha"], repo=dict(full_name="owner/repo", id=42, node_id="R_repo")), head=dict(sha="b" * 40))
            summary.update(spec.get("summary", {}))
            values = dict(summary=summary, files=[dict(filename=path) for path in spec.get("files", [])],
                          closing_issues=[dict(repository=dict(repo, database_id=None), identity=dict(kind="issue", number=n, database_id=None, node_id=f"I_{n}"),
                                               url=f"https://github.com/owner/repo/issues/{n}", state="open") for n in spec.get("issues", [])])
            components = {}
            for name, value in values.items():
                payload = json.dumps(value)
                ref = artifact_ref(payload)
                payloads[object_name(ref)] = payload
                components[name] = dict(status="partial" if name == spec.get("partial") else "complete", fetched_at=now,
                                        source=dict(transport="rest", resource=f"/repos/owner/repo/pulls/{number}"), revision=revision,
                                        expected_count=None, received_count=len(value) if isinstance(value, list) else 0,
                                        pagination_complete=True, truncated=False, error="interrupted" if name == spec.get("partial") else None, object=ref)

            if spec.get("missing"):
                components[spec["missing"]].update(status="unavailable", object=None, error="not fetched")
            if identity["kind"] == "issue":
                for name in ("files", "closing_issues"):
                    components[name].update(status="not_applicable", source=None, fetched_at=None, object=None, expected_count=None, received_count=None, pagination_complete=None)

            rows.append(dict(identity=identity, revision=revision, components=components))

        manifest = seal_snapshot(dict(schema_version=1, artifact="evidence-snapshot", repository=repo,
                                                   started_at=now, completed_at=now, requested_components=["summary", "files", "closing_issues"], items=rows))
        script = """
import json, sys
sys.path.insert(0, 'bin')
from _cache import EvidenceCache
manifest, payloads = json.load(sys.stdin)
cache = EvidenceCache(manifest['repository'])
cache.initialize()
cache.publish(manifest, payloads)
"""
        process = subprocess.run([sys.executable, "-c", script], input=json.dumps([manifest, payloads]), cwd=self.root, env=self.env, text=True, capture_output=True)
        self.assertEqual(process.returncode, 0, process.stderr)
        return manifest["snapshot_id"]

    def report(self, snapshot, *args):
        return json.loads(self.cli("cache", "candidates", "--snapshot", snapshot, *args).stdout)

    def save_candidate(self, packet, identifier=None, ok=True):
        path = self.root / "candidate-packet.json"
        path.write_text(json.dumps(packet))
        identifier = identifier or packet["results"][0]["id"]
        return self.cli("group", "create-candidate", "--file", str(path), "--candidate", identifier, "--by", "reviewer", ok=ok)


