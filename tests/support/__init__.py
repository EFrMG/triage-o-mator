"""Shared fixtures for Python tests and Go test setup scripts."""

from .base import AcquisitionFixture, CheckoutTest, FAKE, FAKE_GH, ROOT, comment, item, repo, summary
from .discovery import CandidateFixture, ChunkFixture, CorpusFixture, InventoryFixture
from .evidence import DIFF, HEAD, PATCH, EvidenceFixture, example
from .watch import CLAIM, ClosureStoreFixture, WatchFixture
from .base import artifact_ref, object_name, repository, seal_snapshot
