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


if __name__ == "__main__":
    main()
