"""Behavioral safety contract for owned-PR hold reconciliation and compiler refresh."""

import copy
import importlib.util
import io
import json
import os
import subprocess
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest import TestCase, main
from unittest.mock import patch


spec = importlib.util.spec_from_file_location(
    "agentic_pr_maintenance", Path(__file__).with_name("agentic_pr_maintenance.py")
)
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)

REPO = "Azure/ARO-HCP"
NUMBER = 7597
HEAD = "d071a83f32db9a874ed681ae192ed18ce7bc5bf0"
OLDER = "a" * 40
OTHER = "b" * 40


def comment(identifier, body, author="openshift-merge-bot[bot]"):
    posted = (datetime(2026, 10, 25, tzinfo=timezone.utc) + timedelta(seconds=identifier)).isoformat()
    return {
        "id": identifier, "user": {"login": author}, "body": body,
        "created_at": posted, "updated_at": posted,
    }


def retry_hold(identifier=1, sha=OLDER):
    return comment(identifier, f"/hold\n\nRevision {sha} was retested 3 times: holding")


def status(state="success", identifier=1, context="ci/prow/e2e-parallel"):
    return {
        "id": identifier, "context": context, "state": state,
        "created_at": comment(identifier, "")["created_at"],
    }


def check(conclusion="success", state="completed", name="ci/pipeline-gate"):
    return {"id": 100, "name": name, "head_sha": HEAD, "status": state, "conclusion": conclusion}


def snapshot(dependency=False):
    labels = [{"name": "do-not-merge/hold"}]
    if dependency:
        labels.append({"name": "agentic-dependabot"})
    return {
        "pull": {
            "number": NUMBER, "state": "open", "draft": False, "merged": False,
            "user": {"login": "aro-hcp-robot[bot]"},
            "title": "fix(deps): upgrade undici (AROSLSRE-1234)" if dependency else guard.UPGRADE_TITLE,
            "head": {
                "sha": HEAD, "ref": "agentic-dependabot-undici" if dependency else "upgrade-agentic-workflows-42",
                "repo": {"full_name": REPO},
            },
            "base": {"ref": "main", "repo": {"full_name": REPO}},
            "labels": labels,
        },
        "comments": [retry_hold()], "commits": [OLDER, HEAD],
        "statuses": [status()], "checks": [check()],
        "hold_events": [{
            "id": 1, "event": "labeled", "actor": {"login": "openshift-ci[bot]"},
            "created_at": comment(1, "")["created_at"],
        }],
    }


def assess(data, expected=HEAD):
    return guard.assess(data, REPO, NUMBER, expected)


class OwnershipTests(TestCase):
    def test_exact_owned_upgrade_and_dependency_are_recognized(self):
        for dependency, kind in ((False, "upgrade"), (True, "dependency")):
            with self.subTest(kind=kind):
                result = assess(snapshot(dependency))
                self.assertEqual(result["kind"], kind)
                self.assertTrue(result["can_release_hold"])
                self.assertEqual(result["can_refresh_upgrade"], not dependency)

    def test_unowned_author_fork_base_title_or_branch_is_rejected(self):
        mutations = [
            lambda p: p["user"].update(login="human"),
            lambda p: p["user"].update(login="app/aro-hcp-robot"),
            lambda p: p["head"]["repo"].update(full_name="attacker/ARO-HCP"),
            lambda p: p["base"]["repo"].update(full_name="attacker/ARO-HCP"),
            lambda p: p["base"].update(ref="release"),
            lambda p: p.update(title=guard.UPGRADE_TITLE + " extra"),
            lambda p: p["head"].update(ref="upgrade-agentic-workflows-"),
            lambda p: p["head"].update(ref="other-upgrade-42"),
        ]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                data = snapshot()
                mutate(data["pull"])
                result = assess(data)
                self.assertIsNone(result["kind"])
                self.assertFalse(result["can_release_hold"])
                self.assertFalse(result["can_refresh_upgrade"])

    def test_dependency_requires_both_label_and_title(self):
        for missing in ("label", "title"):
            with self.subTest(missing=missing):
                data = snapshot(dependency=True)
                if missing == "label":
                    data["pull"]["labels"] = [{"name": guard.HOLD_LABEL}]
                else:
                    data["pull"]["title"] = "chore(deps): update undici"
                self.assertFalse(assess(data)["can_release_hold"])

    def test_closed_merged_draft_or_wrong_number_is_rejected(self):
        for field, value in (("state", "closed"), ("merged", True), ("draft", True), ("number", NUMBER + 1)):
            with self.subTest(field=field):
                data = snapshot()
                data["pull"][field] = value
                self.assertFalse(assess(data)["can_release_hold"])
                self.assertFalse(assess(data)["can_refresh_upgrade"])

    def test_stale_expected_head_rejects_release_and_refresh(self):
        data = snapshot()
        data["checks"] = []
        result = assess(data, OLDER)
        self.assertFalse(result["can_release_hold"])
        self.assertFalse(result["can_refresh_upgrade"])
        self.assertIn("current head does not match", " ".join(result["release_blockers"]))


class HoldLifecycleTests(TestCase):
    def test_unknown_human_or_inconsistent_hold_label_blocks_recovery(self):
        for provenance in ("missing", "human", "unknown-bot", "removed"):
            with self.subTest(provenance=provenance):
                data = snapshot()
                if provenance == "missing":
                    data["hold_events"] = []
                elif provenance == "removed":
                    data["hold_events"][0]["event"] = "unlabeled"
                else:
                    data["hold_events"][0]["actor"]["login"] = provenance
                result = assess(data)
                self.assertFalse(result["can_release_hold"])
                self.assertFalse(result["can_refresh_upgrade"])

    def test_human_hold_before_later_bot_hold_still_blocks(self):
        data = snapshot()
        data["comments"] = [retry_hold(2), comment(1, "/hold\nInvestigating", "human")]
        result = assess(data)
        self.assertEqual([h["kind"] for h in result["active_holds"]], ["human", "retry-exhaustion"])
        self.assertFalse(result["can_release_hold"])
        self.assertFalse(result["can_refresh_upgrade"])

    def test_same_human_cancel_resolves_hold_in_chronological_order(self):
        for cancel in ("/hold cancel", "/HOLD\tCANCEL reason", "/unhold", "/remove-hold"):
            with self.subTest(cancel=cancel):
                data = snapshot()
                data["comments"] = [comment(3, cancel, "human"), retry_hold(2), comment(1, "/hold", "human")]
                result = assess(data)
                self.assertTrue(result["can_release_hold"])
                self.assertEqual(len(result["active_holds"]), 1)

    def test_cancel_does_not_resolve_later_hold(self):
        data = snapshot()
        data["comments"] += [comment(2, "/hold cancel", "human"), comment(3, "/hold", "human")]
        self.assertFalse(assess(data)["can_release_hold"])

    def test_other_human_or_bot_cancel_cannot_erase_human_hold(self):
        for author in ("other-human", guard.ROBOT, guard.MERGE_BOT):
            with self.subTest(author=author):
                data = snapshot()
                data["comments"] += [comment(2, "/hold", "human"), comment(3, "/hold cancel", author)]
                self.assertFalse(assess(data)["can_release_hold"])
                self.assertFalse(assess(data)["can_refresh_upgrade"])

    def test_prow_cancel_has_precedence_within_same_comment(self):
        data = snapshot()
        data["comments"] += [comment(2, "/hold", "human"), comment(3, "/hold cancel\n/hold", "human")]
        self.assertTrue(assess(data)["can_release_hold"])

    def test_retry_text_from_human_or_different_bot_is_not_automated(self):
        for author in ("human", "other[bot]", guard.ROBOT):
            with self.subTest(author=author):
                data = snapshot()
                data["comments"][0]["user"]["login"] = author
                self.assertFalse(assess(data)["can_release_hold"])
                self.assertFalse(assess(data)["can_refresh_upgrade"])

    def test_bot_hold_requires_exact_retry_message_and_evidenced_sha(self):
        for body in ("/hold", f"/hold\nRevision {OLDER} failed", retry_hold(sha=OTHER)["body"]):
            with self.subTest(body=body):
                data = snapshot()
                data["comments"] = [comment(1, body)]
                self.assertEqual(assess(data)["active_holds"][0]["kind"], "unexplained")
                self.assertFalse(assess(data)["can_release_hold"])

    def test_edited_retry_or_cancel_is_not_trusted(self):
        for edited_cancel in (False, True):
            with self.subTest(edited_cancel=edited_cancel):
                data = snapshot()
                if edited_cancel:
                    data["comments"].append(comment(2, "/hold cancel", guard.ROBOT))
                data["comments"][-1]["updated_at"] = comment(10, "")["created_at"]
                self.assertFalse(assess(data)["can_release_hold"])
                self.assertFalse(assess(data)["can_refresh_upgrade"])

    def test_unexplained_label_blocks_but_absent_label_is_noop(self):
        data = snapshot()
        data["comments"] = []
        self.assertFalse(assess(data)["can_release_hold"])
        self.assertFalse(assess(data)["can_refresh_upgrade"])
        data["pull"]["labels"] = []
        result = assess(data)
        self.assertEqual(result["release_status"], "not-held")
        self.assertFalse(result["can_release_hold"])
        self.assertTrue(result["can_refresh_upgrade"])

    def test_duplicate_holds_and_duplicate_pages_remain_one_release_decision(self):
        data = snapshot()
        data["comments"] += [copy.deepcopy(data["comments"][0]), retry_hold(2)]
        result = assess(data)
        self.assertTrue(result["can_release_hold"])
        self.assertEqual(len(result["active_holds"]), 2)
        data["comments"].append(comment(3, "/hold cancel", guard.ROBOT))
        result = assess(data)
        self.assertEqual(result["active_holds"], [])
        self.assertEqual(result["release_status"], "cancel-pending")
        self.assertFalse(result["can_release_hold"])

    def test_conflicting_duplicate_comment_evidence_fails_closed(self):
        data = snapshot()
        data["comments"].append(comment(1, "/hold", "human"))
        with self.assertRaisesRegex(ValueError, "conflicting duplicate"):
            assess(data)


class CITests(TestCase):
    def test_pr7597_same_head_failed_ci_cannot_release_but_can_refresh(self):
        data = snapshot()
        data["comments"] = [retry_hold(sha=HEAD)]
        data["statuses"] = [status("failure")]
        result = assess(data)
        self.assertFalse(result["can_release_hold"])
        self.assertTrue(result["can_refresh_upgrade"])
        self.assertEqual(result["active_holds"][0]["sha"], HEAD)

    def test_other_green_checks_do_not_replace_required_pipeline_evidence(self):
        for missing in ("e2e", "pipeline"):
            with self.subTest(missing=missing):
                data = snapshot()
                if missing == "e2e":
                    data["statuses"] = [status(context="license/cla")]
                else:
                    data["checks"] = [check(name="verify")]
                self.assertFalse(assess(data)["can_release_hold"])

    def test_pending_or_failed_status_blocks_even_with_other_success(self):
        for state in ("pending", "failure", "error", "unknown", None):
            with self.subTest(state=state):
                data = snapshot()
                data["statuses"] = [status(state)]
                self.assertFalse(assess(data)["can_release_hold"])

    def test_pending_or_unsuccessful_check_blocks_even_with_positive_status(self):
        for state, conclusion in (
            ("queued", None), ("in_progress", None), ("waiting", None),
            ("completed", "failure"), ("completed", "cancelled"),
            ("completed", "timed_out"), ("completed", "action_required"),
            ("completed", "startup_failure"), ("completed", None),
        ):
            with self.subTest(state=state, conclusion=conclusion):
                data = snapshot()
                data["checks"] = [check(conclusion, state)]
                self.assertFalse(assess(data)["can_release_hold"])

    def test_same_head_success_allows_release_and_tide_is_not_ci(self):
        data = snapshot()
        data["comments"] = [retry_hold(sha=HEAD)]
        data["statuses"] += [status("pending", 2, "tide")]
        data["checks"] += [check(None, "queued", "TiDe")]
        self.assertTrue(assess(data)["can_release_hold"])
        self.assertNotIn("tide", assess(data)["ci"]["positive"])

    def test_tide_only_empty_or_neutral_ci_is_not_positive_evidence(self):
        for statuses, checks in (([], []), ([status("success", context="tide")], []), ([], [check("skipped")]), ([], [check("neutral")])):
            with self.subTest(statuses=statuses, checks=checks):
                data = snapshot()
                data.update(statuses=statuses, checks=checks)
                self.assertFalse(assess(data)["can_release_hold"])
                self.assertTrue(assess(data)["can_refresh_upgrade"])

    def test_newest_status_per_context_wins_not_api_array_order(self):
        data = snapshot()
        data["statuses"] = [status("success", 3), status("failure", 1), status("pending", 2)]
        self.assertTrue(assess(data)["can_release_hold"])
        data["statuses"].append(status("failure", 4))
        self.assertFalse(assess(data)["can_release_hold"])

    def test_status_id_breaks_equal_timestamp_ties(self):
        data = snapshot()
        success, failure = status("success", 2), status("failure", 1)
        success["created_at"] = failure["created_at"]
        data["statuses"] = [success, failure]
        self.assertTrue(assess(data)["can_release_hold"])

    def test_wrong_head_check_evidence_fails_closed(self):
        data = snapshot()
        data["checks"][0]["head_sha"] = OLDER
        with self.assertRaisesRegex(ValueError, "expected head"):
            assess(data)


class ExecutionTests(TestCase):
    def test_successful_release_is_a_single_idempotent_comment_transition(self):
        server = snapshot()

        def submit(args, **kwargs):
            if args[:4] != ["gh", "api", "--method", "POST"]:
                raise AssertionError("Unexpected mutation method")
            if args[4] != f"repos/{REPO}/issues/{NUMBER}/comments":
                raise AssertionError("Unexpected mutation target")
            posted = comment(99, json.loads(kwargs["input"])["body"], guard.ROBOT)
            server["comments"].append(posted)
            return subprocess.CompletedProcess(args, 0, json.dumps(posted), "")

        with patch.object(guard, "fetch_snapshot", side_effect=lambda *args: copy.deepcopy(server)):
            with patch.object(guard.subprocess, "run", side_effect=submit):
                requested = guard.execute("release-hold", REPO, NUMBER, HEAD)
                repeated = guard.execute("release-hold", REPO, NUMBER, HEAD)
        self.assertEqual(requested["release_status"], "cancel-requested")
        self.assertEqual(repeated["release_status"], "cancel-pending")
        self.assertEqual([c["body"] for c in server["comments"][1:]], ["/hold cancel"])

    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Unauthorized mutation"))
    @patch.object(guard, "fetch_snapshot")
    def test_fresh_head_human_hold_or_ci_change_blocks_write(self, fetch, run):
        for changed in ("head", "human", "ci"):
            with self.subTest(changed=changed):
                fresh = snapshot()
                if changed == "head":
                    fresh["pull"]["head"]["sha"] = OTHER
                elif changed == "human":
                    fresh["comments"].append(comment(2, "/hold", "human"))
                else:
                    fresh["statuses"] = [status("pending")]
                fetch.side_effect = [snapshot(), fresh]
                with self.assertRaises(ValueError):
                    guard.execute("release-hold", REPO, NUMBER, HEAD)

    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Duplicate mutation"))
    @patch.object(guard, "fetch_snapshot")
    def test_missing_label_or_existing_cancel_is_idempotent(self, fetch, run):
        for no_label in (False, True):
            with self.subTest(no_label=no_label):
                data = snapshot()
                if no_label:
                    data["pull"]["labels"] = []
                else:
                    data["comments"].append(comment(2, "/hold cancel", guard.ROBOT))
                fetch.return_value = data
                result = guard.execute("release-hold", REPO, NUMBER, HEAD)
                self.assertIn(result["release_status"], ("not-held", "cancel-pending"))

    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Duplicate mutation"))
    @patch.object(guard, "fetch_snapshot")
    def test_cancel_arriving_during_recheck_prevents_duplicate_post(self, fetch, run):
        fresh = snapshot()
        fresh["comments"].append(comment(2, "/hold cancel", guard.ROBOT))
        fetch.side_effect = [snapshot(), fresh]
        result = guard.execute("release-hold", REPO, NUMBER, HEAD)
        self.assertEqual(result["release_status"], "cancel-pending")

    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Read-only mode mutated"))
    @patch.object(guard, "fetch_snapshot")
    def test_assessment_and_refresh_guard_are_read_only_even_with_red_ci(self, fetch, run):
        data = snapshot()
        data["statuses"] = [status("failure")]
        fetch.return_value = data
        for mode in ("assess", "verify-upgrade"):
            result = guard.execute(mode, REPO, NUMBER, HEAD)
            self.assertFalse(result["can_release_hold"])
            self.assertTrue(result["can_refresh_upgrade"])

    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Unauthorized mutation"))
    @patch.object(guard, "fetch_snapshot")
    def test_dependency_or_human_hold_cannot_pass_compiler_refresh_guard(self, fetch, run):
        for data in (snapshot(dependency=True), snapshot()):
            if "agentic-dependabot" not in {label["name"] for label in data["pull"]["labels"]}:
                data["comments"].append(comment(2, "/hold", "human"))
            fetch.return_value = data
            with self.assertRaises(ValueError):
                guard.execute("verify-upgrade", REPO, NUMBER, HEAD)

    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Mutation after failed read"))
    @patch.object(guard, "fetch_snapshot")
    def test_api_failure_at_initial_or_final_read_never_posts(self, fetch, run):
        error = subprocess.CalledProcessError(1, ["gh", "api"])
        for responses in ([error], [snapshot(), error]):
            with self.subTest(reads=len(responses)):
                fetch.side_effect = responses
                with self.assertRaises(subprocess.CalledProcessError):
                    guard.execute("release-hold", REPO, NUMBER, HEAD)

    @patch.object(guard.subprocess, "run", side_effect=[subprocess.CalledProcessError(1, ["gh", "api"]), AssertionError("Unexpected retry")])
    @patch.object(guard, "fetch_snapshot", side_effect=lambda *args: snapshot())
    def test_mutation_api_failure_is_not_retried(self, _fetch, run):
        with self.assertRaises(subprocess.CalledProcessError):
            guard.execute("release-hold", REPO, NUMBER, HEAD)

    @patch.object(guard.subprocess, "run")
    @patch.object(guard, "fetch_snapshot", side_effect=lambda *args: snapshot())
    def test_malformed_mutation_response_is_reported_not_retried(self, _fetch, run):
        run.side_effect = [subprocess.CompletedProcess([], 0, '{"message":"bad response"}', ""), AssertionError("Unexpected retry")]
        with self.assertRaises(ValueError):
            guard.execute("release-hold", REPO, NUMBER, HEAD)


class APITests(TestCase):
    @patch.dict(os.environ, {"CI_TOKEN": "ci-reader"})
    @patch.object(guard, "gh_lines")
    def test_rest_snapshot_recognizes_held_failed_revision_as_refreshable(self, gh):
        data = snapshot()
        data["comments"] = [retry_hold(sha=HEAD)]
        data["statuses"] = [status("failure")]
        gh.side_effect = [
            [data["pull"]], [{"sha": sha} for sha in data["commits"]],
            data["statuses"], data["checks"], data["comments"], data["hold_events"], [data["pull"]],
        ]
        result = assess(guard.fetch_snapshot(REPO, NUMBER, HEAD))
        self.assertEqual(result["active_holds"][0]["kind"], "retry-exhaustion")
        self.assertEqual(result["active_holds"][0]["sha"], HEAD)
        self.assertTrue(result["can_refresh_upgrade"])
        self.assertFalse(result["can_release_hold"])


    @patch.dict(os.environ, {"CI_TOKEN": "ci-reader"})
    @patch.object(guard, "gh_lines")
    def test_head_change_within_snapshot_fails_closed(self, gh):
        data = snapshot()
        changed = copy.deepcopy(data["pull"])
        changed["head"]["sha"] = OTHER
        gh.side_effect = [[data["pull"]], [{"sha": sha} for sha in data["commits"]], data["statuses"], data["checks"], data["comments"], data["hold_events"], [changed]]
        with self.assertRaisesRegex(ValueError, "head changed"):
            guard.fetch_snapshot(REPO, NUMBER, HEAD)


    @patch.object(guard.subprocess, "run")
    def test_malformed_api_json_fails_closed(self, run):
        run.return_value = subprocess.CompletedProcess([], 0, "not json", "")
        with self.assertRaises(ValueError):
            guard.gh_lines("repos/example/checks", ".[]")

    @patch.object(guard, "gh_lines", return_value=[])
    def test_empty_pull_api_response_fails_closed(self, _gh):
        with self.assertRaisesRegex(ValueError, "incomplete evidence"):
            guard.fetch_pull(REPO, NUMBER)

    def test_missing_required_snapshot_evidence_fails_closed(self):
        for field in ("pull", "comments", "commits", "statuses", "checks", "hold_events"):
            with self.subTest(field=field):
                data = snapshot()
                del data[field]
                with self.assertRaises(ValueError):
                    assess(data)

    @patch.object(guard, "fetch_snapshot", side_effect=AssertionError("Invalid target reached API"))
    def test_invalid_cli_target_is_rejected_before_api_access(self, fetch):
        for repo, number, head in (("../ARO-HCP", NUMBER, HEAD), (REPO, "1/../../issues", HEAD), (REPO, NUMBER, "main")):
            with self.subTest(repo=repo, number=number, head=head):
                with self.assertRaises(ValueError):
                    guard.execute("release-hold", repo, number, head)

    @patch.object(guard, "execute", side_effect=subprocess.CalledProcessError(1, ["gh", "api"]))
    def test_main_returns_failure_without_success_json_on_api_error(self, _execute):
        with patch("sys.stdout", new_callable=io.StringIO) as out, patch("sys.stderr", new_callable=io.StringIO) as err:
            self.assertEqual(guard.main(["release-hold", REPO, str(NUMBER), HEAD]), 1)
            self.assertEqual(out.getvalue(), "")

    @patch.object(guard, "fetch_snapshot", side_effect=lambda *args: snapshot())
    @patch.object(guard, "fetch_pull", side_effect=lambda *args: snapshot()["pull"])
    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Read-only mode mutated"))
    def test_inspect_discovers_head_and_prints_read_only_json(self, run, _pull, fetch):
        with patch("sys.stdout", new_callable=io.StringIO) as out:
            self.assertEqual(guard.main(["inspect", REPO, str(NUMBER)]), 0)
        result = json.loads(out.getvalue())
        self.assertEqual(result["expected_head"], HEAD)
        self.assertEqual(result["kind"], "upgrade")


class RecoveryTests(TestCase):
    failure_url = "https://prow.ci.openshift.org/view/gs/test/failing/123"
    comparison_url = "https://prow.ci.openshift.org/view/gs/test/main/124"
    evidence = (
        "The failing run shows an infrastructure DNS outage also present on main: "
        f"{failure_url} compared with {comparison_url}."
    )

    def failed(self):
        data = snapshot()
        data["comments"] = [retry_hold(sha=HEAD)]
        data["statuses"] = [dict(status("failure"), target_url=self.failure_url)]
        return data

    def test_failed_same_head_is_recoverable_but_not_green_release(self):
        result = assess(self.failed())
        self.assertTrue(result["can_recover_retry"])
        self.assertFalse(result["can_release_hold"])

    def test_pending_unknown_or_missing_failure_evidence_prevents_recovery(self):
        for change in ("pending-status", "pending-check", "unknown", "no-url", "green", "human"):
            with self.subTest(change=change):
                data = self.failed()
                if change == "pending-status":
                    data["statuses"].append(status("pending", 2, "ci/other"))
                elif change == "pending-check":
                    data["checks"].append(check(None, "queued", "ci/other"))
                elif change == "unknown":
                    data["statuses"].append(status("unknown", 2, "ci/other"))
                elif change == "no-url":
                    del data["statuses"][0]["target_url"]
                elif change == "green":
                    data["statuses"][0]["state"] = "success"
                else:
                    data["comments"].append(comment(2, "/hold", "human"))
                self.assertFalse(assess(data)["can_recover_retry"])

    def test_recovery_posts_only_fixed_commands_and_is_permanently_bounded(self):
        server = self.failed()
        calls = []

        def submit(args, **kwargs):
            calls.append(args)
            body = json.loads(kwargs["input"])["body"]
            posted = comment(99, body, guard.ROBOT)
            server["comments"].append(posted)
            return subprocess.CompletedProcess(args, 0, json.dumps(posted), "")

        with patch.object(guard, "fetch_snapshot", side_effect=lambda *args: copy.deepcopy(server)):
            with patch.object(guard.subprocess, "run", side_effect=submit):
                result = guard.execute("recover-retry", REPO, NUMBER, HEAD, self.evidence)
                pending = guard.execute("recover-retry", REPO, NUMBER, HEAD, self.evidence)
                self.assertEqual(pending["release_status"], "cancel-pending")
                # A new retry hold at the same revision cannot reset the marker.
                server["comments"].append(retry_hold(100, HEAD))
                with self.assertRaisesRegex(ValueError, "already consumed"):
                    guard.execute("recover-retry", REPO, NUMBER, HEAD, self.evidence)
        self.assertEqual(result["release_status"], "cancel-requested")
        self.assertEqual(len(calls), 1)
        self.assertIn(f"<!-- {guard.RECOVERY_MARKER}{HEAD} -->", server["comments"][1]["body"])
        self.assertTrue(server["comments"][1]["body"].endswith("/hold cancel\n/retest-required"))

    def test_unknown_marker_author_does_not_consume_bot_budget(self):
        data = self.failed()
        data["comments"].append(comment(2, f"<!-- {guard.RECOVERY_MARKER}{HEAD} -->", "human"))
        self.assertTrue(assess(data)["can_recover_retry"])

    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Unauthorized mutation"))
    def test_invalid_missing_comparison_or_command_evidence_is_rejected(self, run):
        with patch.object(guard, "fetch_snapshot", side_effect=lambda *args: self.failed()):
            for evidence in (
                "", self.failure_url, self.evidence.replace(self.failure_url, self.comparison_url),
                self.evidence + "\n/approve", self.evidence + f"\n{guard.RECOVERY_MARKER}",
                self.evidence.replace(self.comparison_url, "https://attacker.example/comparison"),
            ):
                with self.subTest(evidence=evidence), self.assertRaises(ValueError):
                    guard.execute("recover-retry", REPO, NUMBER, HEAD, evidence)

    @patch.object(guard.subprocess, "run", side_effect=AssertionError("Unauthorized mutation"))
    def test_fresh_pending_human_hold_or_new_head_blocks_recovery(self, run):
        for change in ("pending", "human", "head"):
            fresh = self.failed()
            if change == "pending":
                fresh["statuses"].append(status("pending", 2, "ci/other"))
            elif change == "human":
                fresh["comments"].append(comment(2, "/hold", "human"))
            else:
                fresh["pull"]["head"]["sha"] = OTHER
            with patch.object(guard, "fetch_snapshot", side_effect=[self.failed(), fresh]):
                with self.subTest(change=change), self.assertRaises(ValueError):
                    guard.execute("recover-retry", REPO, NUMBER, HEAD, self.evidence)

    def test_real_compiler_regeneration_can_release_before_fresh_ci_is_green(self):
        data = snapshot()
        data["statuses"] = [status("pending")]
        data["checks"] = [check(None, "queued")]

        def submit(args, **kwargs):
            if args[:2] == ["git", "merge-base"]:
                return subprocess.CompletedProcess(args, 0, "", "")
            if args[:2] == ["git", "diff"]:
                return subprocess.CompletedProcess(args, 0, ".github/workflows/gh-aw-recompile.lock.yml\n", "")
            body = json.loads(kwargs["input"])["body"]
            return subprocess.CompletedProcess(args, 0, json.dumps(comment(99, body, guard.ROBOT)), "")

        with patch.object(guard, "fetch_snapshot", side_effect=lambda *args: copy.deepcopy(data)):
            with patch.object(guard, "compiler_versions", side_effect=lambda ref: [
                "v0.89.20" if ref == OLDER else "v0.89.21"
            ]):
                with patch.object(guard.subprocess, "run", side_effect=submit) as run:
                    result = guard.execute("release-regenerated", REPO, NUMBER, HEAD, OLDER)
        self.assertEqual(result["release_status"], "cancel-requested")
        self.assertNotIn("/retest-required", json.loads(run.call_args.kwargs["input"])["body"])

    def test_unchanged_regeneration_or_human_hold_cannot_unhold(self):
        for change in ("same-head", "human", "same-version"):
            data = snapshot()
            if change == "human":
                data["comments"].append(comment(2, "/hold", "human"))
            with patch.object(guard, "fetch_snapshot", return_value=data):
                with patch.object(guard, "compiler_versions", return_value=["v0.89.21"]):
                    with patch.object(guard.subprocess, "run", return_value=subprocess.CompletedProcess(
                        [], 0, ".github/workflows/test.lock.yml\n", "",
                    )) as run:
                        with self.subTest(change=change), self.assertRaises(ValueError):
                            guard.execute("release-regenerated", REPO, NUMBER, HEAD, HEAD if change == "same-head" else OLDER)
                        self.assertFalse(any(call.args[0][0] == "gh" for call in run.call_args_list))


class CompilerProvenanceTests(TestCase):
    def test_all_lock_versions_are_read_without_checkout(self):
        def git(args, **kwargs):
            if args[1] == "ls-tree":
                text = ".github/workflows/one.lock.yml\n.github/workflows/two.lock.yml\n.github/workflows/source.md\n"
            else:
                version = "v0.89.21" if "one.lock" in args[-1] else "v0.89.20"
                text = '# gh-aw-metadata: ' + json.dumps({"compiler_version": version}) + "\n"
            return subprocess.CompletedProcess(args, 0, text, "")
        with patch.object(guard.subprocess, "run", side_effect=git):
            self.assertEqual(guard.compiler_versions("origin/main"), ["v0.89.20", "v0.89.21"])

    def test_missing_compiler_provenance_is_a_failure_not_a_noop(self):
        with patch.object(guard.subprocess, "run", side_effect=[
            subprocess.CompletedProcess([], 0, ".github/workflows/test.lock.yml\n", ""),
            subprocess.CompletedProcess([], 0, "name: untrusted\n", ""),
        ]):
            with self.assertRaisesRegex(ValueError, "missing compiler provenance"):
                guard.compiler_versions("origin/main")


class WorkflowLifecycleTests(TestCase):
    def test_one_daily_agentic_identity_with_independent_bootstrap(self):
        workflows = Path(__file__).parents[1] / "workflows"
        source = (workflows / "gh-aw-recompile.md").read_text()
        self.assertIn("schedule: daily", source)
        self.assertIn("needs: [recompile]", source)
        self.assertIn("needs: []", source)
        self.assertIn("engine: copilot", source)
        self.assertIn("report-as-issue: false", source)
        for obsolete in ("gh-aw-recompile.yml", "agentic-pr-maintenance.md", "agentic-pr-maintenance.lock.yml"):
            self.assertFalse((workflows / obsolete).exists(), obsolete)


if __name__ == "__main__":
    main()
