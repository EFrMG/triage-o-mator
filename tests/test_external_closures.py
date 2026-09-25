"""Pure closure contracts use fabricated descriptors only; no install, network or payload audit."""

import copy
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "bin"))
try:
    from _evidence import artifact_ref, canonical, digest
    from _external_closures import append_entry, checksum, content_digest, create_history, operation_id, source_pin, validate_history
finally:
    sys.path.pop(0)


AT = "2026-09-23T12:00:00Z"
REPOSITORY = dict(host="github.com", full_name="owner/repo", database_id=10, node_id="R_10")
ITEM = dict(kind="pr", number=1, database_id=20, node_id="PR_20")


def fixture(event_id=99, state="closed"):
    summary = dict(id=20, node_id="PR_20", state=state, closed_at=AT, updated_at=AT)
    event = dict(id=event_id, event="closed", created_at=AT, actor=None)
    comment = dict(id=30, body="Supplied explanation", created_at=AT, updated_at=AT)
    sources = [source_pin("summary", 0, artifact_ref(canonical(summary)), summary),
               source_pin("timeline", 0, artifact_ref(canonical([event])), event),
               source_pin("comments", 0, artifact_ref(canonical([comment])), comment)]
    observation = dict(snapshot_id="a" * 64, observed_at=AT,
                       revision=dict(updated_at=AT, base_sha="b" * 40, head_sha="c" * 40),
                       state=state, closed_at=None if state == "open" else AT, sources=sources,
                       closure_source=1, operation_id=operation_id(REPOSITORY, ITEM, event_id), gaps=[])
    claim = dict(external=dict(namespace="supplied-runner", record_id="record-1"), actor=None, run_id=None,
                 rationale=None, survivor=None, provenance="unknown", supports=[])

    return create_history(REPOSITORY, ITEM), observation, claim


def add(history, observation, claim, **kwargs):
    options = dict(by="operator", at=AT, checkpoint=history["checksum"])
    options.update(kwargs)

    return append_entry(history, observation, claim, **options)


def reseal(history):
    history["checksum"] = checksum(history)

    return history


class ClosureContractTests(unittest.TestCase):
    def test_minimal_unknown_claim_is_not_approval_and_inputs_unchanged(self):
        history, observation, claim = fixture()
        original = copy.deepcopy((history, observation, claim))
        result = add(history, observation, claim)
        self.assertEqual((history, observation, claim), original)
        self.assertEqual(validate_history(result), result)
        self.assertEqual(result["entries"][0]["claim"]["provenance"], "unknown")
        self.assertNotIn("reviewed", canonical(result))
        self.assertNotIn("authority", result)

    def test_exact_retry_preserves_timestamps_even_with_old_checkpoint(self):
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        second = add(first, observation, claim, at="2026-09-24T00:00:00Z", checkpoint=history["checksum"])
        self.assertEqual(first, second)
        self.assertIsNot(first, second)

    def test_changed_external_id_claim_conflicts_without_mutation(self):
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        changed = dict(claim, rationale="Different assertion")
        with self.assertRaisesRegex(ValueError, "conflicts"):
            add(first, observation, changed)
        self.assertEqual(len(first["entries"]), 1)

    def test_correction_and_competing_claim_preserve_original_dissent(self):
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        predecessor = first["entries"][0]["id"]
        corrected = add(first, observation, dict(claim, rationale="Correction"), kind="correction", predecessor=predecessor, reason="Operator corrected attribution")
        competing = add(corrected, observation, dict(claim, rationale="Dissent"), by="other", kind="competing", predecessor=predecessor, reason="Disagree")
        self.assertEqual(competing["entries"][:1], first["entries"])
        self.assertEqual(len(competing["entries"]), 3)
        with self.assertRaisesRegex(ValueError, "already corrected"):
            add(competing, observation, dict(claim, rationale="Overwrite"), kind="correction", predecessor=predecessor, reason="Stale predecessor")

    def test_corrections_can_restore_original_claim_and_distinct_intent_is_retained(self):
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        second = add(first, observation, dict(claim, rationale="Correction B"), kind="correction",
                     predecessor=first["entries"][0]["id"], reason="First correction")
        third = add(second, observation, claim, kind="correction", predecessor=second["entries"][1]["id"], reason="Restore A")
        self.assertEqual(len(third["entries"]), 3)
        self.assertEqual(third["entries"][2]["claim"], first["entries"][0]["claim"])
        fourth = add(third, observation, claim, kind="competing", predecessor=third["entries"][2]["id"], reason="Independently disagree")
        self.assertEqual(len(fourth["entries"]), 4)
        self.assertEqual(add(fourth, observation, claim, kind="competing", predecessor=third["entries"][2]["id"],
                             reason="Independently disagree", checkpoint=third["checksum"], at="2026-09-24T00:00:00Z"), fourth)

    def test_builders_do_not_alias_caller_owned_values(self):
        repository, item = copy.deepcopy(REPOSITORY), copy.deepcopy(ITEM)
        history = create_history(repository, item)
        repository["full_name"] = "changed/name"
        item["number"] = 99
        self.assertEqual(history["repository"], REPOSITORY)
        self.assertEqual(history["item"], ITEM)
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        second = add(first, observation, claim)
        second["entries"][0]["claim"]["external"]["namespace"] = "changed"
        observation["sources"][0]["object"]["bytes"] = 0
        claim["external"]["namespace"] = "changed"
        self.assertEqual(first["entries"][0]["claim"]["external"]["namespace"], "supplied-runner")
        self.assertGreater(first["entries"][0]["observation"]["sources"][0]["object"]["bytes"], 0)
        ref = artifact_ref("[]")
        pin = source_pin("comments", 0, ref, dict(id=1))
        ref["bytes"] = 0
        self.assertEqual(pin["object"]["bytes"], 2)

    def test_one_component_cannot_reference_conflicting_objects(self):
        history, observation, claim = fixture()
        source = copy.deepcopy(observation["sources"][2])
        source.update(offset=1, source_id=31)
        source["object"] = artifact_ref("[]")
        observation["sources"].append(source)
        with self.assertRaisesRegex(ValueError, "one object"):
            add(history, observation, claim)

    def test_changed_source_is_observation_not_claim_correction(self):
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        changed = copy.deepcopy(observation)
        changed["snapshot_id"] = "d" * 64
        edited = dict(id=30, body="Edited explanation", created_at=AT, updated_at="2026-09-24T00:00:00Z")
        changed["sources"][2] = source_pin("comments", 0, artifact_ref(canonical([edited])), edited)
        second = add(first, changed, claim, kind="observation", predecessor=first["entries"][0]["id"], reason="Retain source revision")
        self.assertEqual(second["entries"][0], first["entries"][0])
        self.assertEqual(second["entries"][1]["claim"], claim)
        self.assertNotEqual(second["entries"][0]["observation"]["sources"][2], second["entries"][1]["observation"]["sources"][2])
        with self.assertRaisesRegex(ValueError, "cannot correct"):
            add(first, changed, dict(claim, rationale="Changed claim"), kind="observation", predecessor=first["entries"][0]["id"], reason="Pretend observation")

    def test_stale_checkpoint_and_wrong_predecessor_rejected(self):
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        changed = dict(claim, rationale="Change")
        with self.assertRaisesRegex(ValueError, "checkpoint changed"):
            add(first, observation, changed, checkpoint=history["checksum"], kind="correction", predecessor=first["entries"][0]["id"], reason="Correction")
        with self.assertRaisesRegex(ValueError, "earlier explicit predecessor"):
            add(first, observation, changed, kind="correction", predecessor="f" * 64, reason="Correction")

    def test_external_namespace_and_record_identity_are_preserved(self):
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        changed = dict(claim, external=dict(namespace="another", record_id="record-1"))
        self.assertEqual(len(add(first, observation, changed)["entries"]), 2)
        with self.assertRaisesRegex(ValueError, "external identity"):
            add(first, observation, changed, kind="correction", predecessor=first["entries"][0]["id"], reason="Replace namespace")

    def test_close_reopen_close_distinct_operations_and_historical_open_summary(self):
        history, observation, claim = fixture(state="open")
        first = add(history, observation, claim)
        _, later, _ = fixture(event_id=100)
        self.assertNotEqual(observation["operation_id"], later["operation_id"])
        second = add(first, later, dict(claim, external=None))
        self.assertEqual(len(second["entries"]), 2)
        self.assertEqual(second["entries"][0]["observation"]["state"], "open")
        with self.assertRaisesRegex(ValueError, "cannot correct"):
            add(first, later, claim, kind="observation", predecessor=first["entries"][0]["id"], reason="Different closure")

    def test_merged_is_preserved_and_never_a_summary_only_closure(self):
        history, observation, claim = fixture(state="merged")
        self.assertEqual(add(history, observation, claim)["entries"][0]["observation"]["state"], "merged")
        observation.update(closure_source=None, operation_id=None, gaps=[dict(component="operation", reason="Unknown")])
        with self.assertRaisesRegex(ValueError, "summary-only"):
            add(history, observation, claim)

    def test_summary_fallback_has_unknown_operation_and_explicit_gap(self):
        history, observation, claim = fixture()
        observation.update(sources=observation["sources"][:1], closure_source=None, operation_id=None,
                           gaps=[dict(component="operation", reason="No identified closure event"), dict(component="timeline", reason="Not captured")])
        self.assertIsNone(add(history, observation, claim)["entries"][0]["observation"]["operation_id"])
        for changes in (dict(gaps=[]), dict(state="open"), dict(operation_id="e" * 64)):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                add(history, dict(observation, **changes), claim)

    def test_nonclosure_selector_cannot_identify_operation(self):
        history, observation, claim = fixture()
        for changes in (dict(closure_source=2), dict(closure_source=99), dict(closure_source=True), dict(operation_id="e" * 64)):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                add(history, dict(observation, **changes), claim)
        observation["sources"][1]["event"] = "reopened"
        with self.assertRaisesRegex(ValueError, "closed timeline event"):
            add(history, observation, claim)

    def test_partial_gaps_are_retained_without_claiming_completeness(self):
        history, observation, claim = fixture()
        observation["gaps"] = [dict(component="comments", reason="Pagination stopped")]
        result = add(history, observation, claim)
        self.assertEqual(result["entries"][0]["observation"]["gaps"], observation["gaps"])
        self.assertNotIn("complete", result["entries"][0]["observation"])

    def test_attributed_automation_requires_support_but_does_not_authenticate(self):
        history, observation, claim = fixture()
        claim["provenance"] = "confirmed-external-automation"
        with self.assertRaisesRegex(ValueError, "supporting sources"):
            add(history, observation, claim)
        claim["supports"] = [2]
        self.assertEqual(add(history, observation, claim)["entries"][0]["claim"], claim)
        for supports in ([True], [-1], [3], [2, 2]):
            with self.subTest(supports=supports), self.assertRaises(ValueError):
                add(history, observation, dict(claim, supports=supports))

    def test_fully_bound_host_repository_and_pr_scope(self):
        for target, key, value in (("repository", "database_id", None), ("repository", "node_id", None), ("item", "node_id", None), ("item", "kind", "issue")):
            history, _, _ = fixture()
            history[target][key] = value
            with self.subTest(target=target, key=key), self.assertRaises(ValueError):
                validate_history(reseal(history))
        self.assertNotEqual(operation_id(REPOSITORY, ITEM, 99), operation_id(dict(REPOSITORY, host="other.example"), ITEM, 99))
        history, observation, claim = fixture()
        history["repository"]["database_id"] = 11
        with self.assertRaisesRegex(ValueError, "operation identity"):
            add(reseal(history), observation, claim)

    def test_versions_unknown_fields_and_authority_fields_rejected(self):
        for changes in (dict(schema_version=2), dict(schema_version=True), dict(policy="future"), dict(reviewed=True)):
            history, _, _ = fixture()
            history.update(changes)
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                validate_history(reseal(history))
        history, observation, claim = fixture()
        for changes in (dict(reviewed=True), dict(provenance="bot"), dict(survivor=1), dict(survivor=True), dict(actor="")):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                add(history, observation, dict(claim, **changes))

    def test_invalid_source_descriptors_and_timestamps(self):
        changes = (dict(offset=-1), dict(offset=True), dict(source_digest="bad"), dict(object=dict(sha256="a" * 64, bytes=1, format="diff")),
                   dict(source_id=True), dict(created_at="2026-09-23"), dict(component="files"))
        for change in changes:
            history, observation, claim = fixture()
            observation["sources"][1].update(change)
            with self.subTest(change=change), self.assertRaises(ValueError):
                add(history, observation, claim)

    def test_duplicate_selectors_missing_summary_and_checksum_tampering(self):
        history, observation, claim = fixture()
        for sources in (observation["sources"][1:], observation["sources"] + observation["sources"][:1]):
            with self.assertRaises(ValueError):
                add(history, dict(observation, sources=sources), claim)
        first = add(history, observation, claim)
        first["entries"][0]["claim"]["actor"] = "tampered"
        with self.assertRaisesRegex(ValueError, "checksum"):
            validate_history(first)
        with self.assertRaisesRegex(ValueError, "content digest"):
            validate_history(reseal(first))

    def test_forged_entry_base_history_is_rejected_even_when_resealed(self):
        history, observation, claim = fixture()
        first = add(history, observation, claim)
        entry = first["entries"][0]
        entry["base_checksum"] = "e" * 64
        entry["id"] = digest(canonical({key: value for key, value in entry.items() if key != "id"}))
        with self.assertRaisesRegex(ValueError, "complete predecessor history"):
            validate_history(reseal(first))

    def test_pins_are_structural_and_do_not_claim_payload_audit(self):
        history, observation, claim = fixture()
        # Deliberately absent bytes: the pure validator can only check the descriptor, never assert source truth.
        observation["sources"][1]["object"]["sha256"] = "f" * 64
        self.assertEqual(len(add(history, observation, claim)["entries"]), 1)

if __name__ == "__main__":
    unittest.main()
