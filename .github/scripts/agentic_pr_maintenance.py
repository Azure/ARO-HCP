#!/usr/bin/env python3
"""Guard the daily gh-aw compiler upgrade and bounded owned-PR hold recovery.

inspect/assess are read-only. release-hold requires successful CI. recover-retry
requires a current failure URL, comparison evidence and an unused per-head budget.
release-regenerated is reserved for the trusted recompiler after a validated push.
All mutations re-read ownership, head and hold provenance; merge policy is unchanged.
"""

import json
import os
import re
import subprocess
import sys
from datetime import datetime


ROBOT = "aro-hcp-robot[bot]"
MERGE_BOT = "openshift-merge-bot[bot]"
UPGRADE_TITLE = "chore(gh-aw): [auto] upgrade agentic workflows (AROSLSRE-2267)"
HOLD_LABEL = "do-not-merge/hold"
SHA = re.compile(r"[0-9a-f]{40}")
RETRY_HOLD = re.compile(
    r"/hold\s*\n\s*\nRevision ([0-9a-f]{40}) was retested ([1-9][0-9]*) times: holding\s*"
)
HOLD_COMMAND = re.compile(r"^/hold(?:\s.*)?$", re.IGNORECASE | re.MULTILINE)
CANCEL_COMMAND = re.compile(
    r"^/(?:remove-hold|hold\s+cancel|unhold)(?:\s.*)?$", re.IGNORECASE | re.MULTILINE
)
RECOVERY_MARKER = "gh-aw-recompile:recovery:"


def compiler_versions(ref):
    """Read compiler provenance without running or checking out the target tree."""
    names = subprocess.run(
        ["git", "ls-tree", "-r", "--name-only", ref, ".github/workflows"],
        check=True, capture_output=True, text=True,
    ).stdout.splitlines()
    versions = set()
    for name in names:
        if not name.endswith(".lock.yml"):
            continue
        content = subprocess.run(
            ["git", "show", f"{ref}:{name}"], check=True, capture_output=True, text=True,
        ).stdout
        first = content.splitlines()[0]
        if not first.startswith("# gh-aw-metadata: "):
            raise ValueError(f"missing compiler provenance in {name}")
        version = json.loads(first.removeprefix("# gh-aw-metadata: ")).get("compiler_version")
        if not isinstance(version, str) or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", version):
            raise ValueError(f"invalid compiler provenance in {name}")
        versions.add(version)
    if not versions:
        raise ValueError("no compiled workflow provenance found")
    return sorted(versions)


def validate_target(repo, number, expected_head):
    if not isinstance(repo, str) or not re.fullmatch(
        r"[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*", repo
    ):
        raise ValueError("repository must be OWNER/REPO")
    if isinstance(number, bool) or not re.fullmatch(r"[1-9][0-9]*", str(number)):
        raise ValueError("pull request number must be a positive integer")
    if not isinstance(expected_head, str) or not SHA.fullmatch(expected_head):
        raise ValueError("expected head must be a full lowercase commit SHA")
    return int(number)


def gh_lines(path, expression, token=None):
    env = os.environ.copy()
    if token:
        env["GH_TOKEN"] = token
    result = subprocess.run(
        ["gh", "api", "--paginate", path, "--jq", expression],
        check=True,
        capture_output=True,
        text=True,
        env=env,
    )
    return [json.loads(line) for line in result.stdout.splitlines() if line]


def fetch_pull(repo, number):
    pulls = gh_lines(
        f"repos/{repo}/pulls/{number}",
        "{number, state, draft, merged, title, user: {login: .user.login}, "
        "head: {sha: .head.sha, ref: .head.ref, repo: {full_name: .head.repo.full_name}}, "
        "base: {ref: .base.ref, repo: {full_name: .base.repo.full_name}}, labels: [.labels[] | {name}]}",
    )
    if len(pulls) != 1 or not isinstance(pulls[0], dict):
        raise ValueError("pull request API returned incomplete evidence")
    return pulls[0]


def fetch_snapshot(repo, number, expected_head):
    """Read paginated REST evidence, then re-read PR identity/head/hold labels."""
    number = validate_target(repo, number, expected_head)
    pull = fetch_pull(repo, number)
    commits = [
        row["sha"]
        for row in gh_lines(f"repos/{repo}/pulls/{number}/commits?per_page=100", ".[] | {sha}")
    ]
    statuses = gh_lines(
        f"repos/{repo}/commits/{expected_head}/statuses?per_page=100",
        ".[] | {id, context, state, created_at, target_url}",
        os.environ["CI_TOKEN"],
    )
    checks = gh_lines(
        f"repos/{repo}/commits/{expected_head}/check-runs?per_page=100&filter=latest",
        ".check_runs[] | {id, name, head_sha, status, conclusion, details_url}",
        os.environ["CI_TOKEN"],
    )
    comments = gh_lines(
        f"repos/{repo}/issues/{number}/comments?per_page=100",
        ".[] | {id, user: {login: .user.login}, body, created_at, updated_at}",
    )
    hold_events = gh_lines(
        f"repos/{repo}/issues/{number}/events?per_page=100",
        '.[] | select((.event == "labeled" or .event == "unlabeled") and .label.name == "do-not-merge/hold") | '
        "{id, event, actor: {login: .actor.login}, created_at}",
    )
    latest_pull = fetch_pull(repo, number)
    if pull.get("head") != latest_pull.get("head"):
        raise ValueError("pull request head changed while fetching evidence")
    return {
        "pull": latest_pull,
        "commits": commits,
        "comments": comments,
        "hold_events": hold_events,
        "statuses": statuses,
        "checks": checks,
    }


def owned_kind(pull, repo):
    """Recognize only the two published same-repository robot PR identities."""
    for field in ("user", "head", "base"):
        if not isinstance(pull.get(field), dict):
            raise ValueError(f"missing pull request {field} evidence")
    for field in ("head", "base"):
        if not isinstance(pull[field].get("repo"), dict):
            raise ValueError(f"missing pull request {field} repository")
    if (
        pull["user"].get("login") != ROBOT
        or pull["head"]["repo"].get("full_name") != repo
        or pull["base"]["repo"].get("full_name") != repo
        or pull["base"].get("ref") != "main"
    ):
        return None
    labels = pull.get("labels")
    if not isinstance(labels, list) or any(
        not isinstance(label, dict) or not isinstance(label.get("name"), str) for label in labels
    ):
        raise ValueError("missing pull request label evidence")
    title = pull.get("title")
    branch = pull["head"].get("ref")
    if not isinstance(title, str) or not isinstance(branch, str):
        raise ValueError("missing pull request title or branch")
    if title == UPGRADE_TITLE and re.fullmatch(r"upgrade-agentic-workflows-[A-Za-z0-9_-]+", branch):
        return "upgrade"
    if title.startswith("fix(deps): ") and "agentic-dependabot" in {label["name"] for label in labels}:
        return "dependency"
    return None


def timestamp(value):
    if not isinstance(value, str):
        raise ValueError("missing evidence timestamp")
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if parsed.tzinfo is None:
        raise ValueError("evidence timestamp must include a timezone")
    return parsed


def active_holds(comments, commits):
    """Replay commands without letting a bot supersede an outstanding human hold.

    Cancellations clear the author's own holds. The two trusted bots may also
    cancel evidenced merge-bot retry holds. Other cross-author cancellations do
    not establish permission to release a human hold and are deliberately ignored.
    Edited commands are unexplained holds until explicitly cancelled by their author.
    Prow cancel aliases, case/whitespace, and same-comment cancel precedence apply.
    """
    ordered = {}
    for comment in comments:
        if not isinstance(comment, dict) or not isinstance(comment.get("body"), str):
            raise ValueError("missing comment evidence")
        identifier = comment.get("id")
        if type(identifier) is not int or identifier <= 0:
            raise ValueError("missing comment identity")
        if identifier in ordered and ordered[identifier] != comment:
            raise ValueError("conflicting duplicate comment evidence")
        ordered[identifier] = comment
    active = []
    cancellation_pending = False
    for comment in sorted(ordered.values(), key=lambda item: (timestamp(item.get("created_at")), item["id"])):
        # Prow gives any cancellation in a comment precedence over any hold in it.
        command = "cancel" if CANCEL_COMMAND.search(comment["body"]) else (
            "hold" if HOLD_COMMAND.search(comment["body"]) else None
        )
        if command is None:
            continue
        user = comment.get("user")
        if not isinstance(user, dict) or not isinstance(user.get("login"), str) or not user["login"]:
            raise ValueError("hold command author is unavailable")
        author = user["login"]
        edited = timestamp(comment.get("updated_at")) != timestamp(comment["created_at"])
        retry = RETRY_HOLD.fullmatch(comment["body"])
        known_retry = (
            author == MERGE_BOT and not edited and command == "hold"
            and retry is not None and retry[1] in commits
        )
        if command == "cancel" and not edited:
            remaining = []
            for hold in active:
                own = hold["author"] == author
                bot_retry = author in (ROBOT, MERGE_BOT) and hold["kind"] == "retry-exhaustion"
                if own or bot_retry:
                    cancellation_pending |= bot_retry
                else:
                    remaining.append(hold)
            active = remaining
        else:
            active.append({
                "comment_id": comment["id"],
                "author": author,
                "kind": "retry-exhaustion" if known_retry else (
                    "unexplained" if author in (ROBOT, MERGE_BOT) or edited else "human"
                ),
                "sha": retry[1] if known_retry else None,
            })
    return active, cancellation_pending and not active


def ci_evidence(statuses, checks, expected_head):
    """Require positive non-Tide CI and reject every live failing/unfinished check."""
    latest = {}
    for status in statuses:
        if not isinstance(status, dict) or not isinstance(status.get("context"), str) or not status["context"]:
            raise ValueError("missing status context")
        if type(status.get("id")) is not int or status["id"] <= 0:
            raise ValueError("missing status identity")
        key = status["context"]
        order = (timestamp(status.get("created_at")), status["id"])
        if key not in latest or order > latest[key][0]:
            latest[key] = (order, status)
    positive = []
    blocked = []
    for _, status in latest.values():
        if status["context"].casefold() == "tide":
            continue
        if status.get("state") == "success":
            positive.append(status["context"])
        else:
            blocked.append({
                "name": status["context"], "state": status.get("state"),
                "url": status.get("target_url"),
                "pending": status.get("state") == "pending",
            })
    for check in checks:
        if not isinstance(check, dict) or not isinstance(check.get("name"), str) or not check["name"]:
            raise ValueError("missing check-run name")
        if check.get("head_sha") != expected_head:
            raise ValueError("check-run evidence does not match the expected head")
        if check["name"].casefold() == "tide":
            continue
        if check.get("status") != "completed":
            blocked.append({
                "name": check["name"], "state": check.get("status"),
                "url": check.get("details_url"), "pending": True,
            })
        elif check.get("conclusion") == "success":
            positive.append(check["name"])
        elif check.get("conclusion") not in ("neutral", "skipped"):
            blocked.append({
                "name": check["name"], "state": check.get("conclusion"),
                "url": check.get("details_url"), "pending": False,
            })
    return {"positive": sorted(set(positive)), "blocked": blocked}


def trusted_hold_label(events):
    """Require the current hold label to originate from a trusted Prow controller."""
    recorded = {}
    for event in events:
        if (
            not isinstance(event, dict)
            or type(event.get("id")) is not int
            or event["id"] <= 0
            or event.get("event") not in ("labeled", "unlabeled")
            or not isinstance(event.get("actor"), dict)
            or not isinstance(event["actor"].get("login"), str)
        ):
            raise ValueError("missing hold label provenance")
        timestamp(event.get("created_at"))
        if event["id"] in recorded and recorded[event["id"]] != event:
            raise ValueError("conflicting hold label history")
        recorded[event["id"]] = event
    if not recorded:
        return False
    latest = max(recorded.values(), key=lambda e: (timestamp(e["created_at"]), e["id"]))
    return latest["event"] == "labeled" and latest["actor"]["login"] in ("openshift-ci[bot]", MERGE_BOT)


def assess(snapshot, repo, number, expected_head):
    """Assess complete REST evidence for ownership, hold provenance, and current-head CI."""
    number = validate_target(repo, number, expected_head)
    if not isinstance(snapshot, dict) or not isinstance(snapshot.get("pull"), dict):
        raise ValueError("missing pull request snapshot")
    for field in ("comments", "commits", "statuses", "checks", "hold_events"):
        if not isinstance(snapshot.get(field), list):
            raise ValueError(f"missing {field} evidence")
    commits = snapshot["commits"]
    if not commits or any(not isinstance(sha, str) or not SHA.fullmatch(sha) for sha in commits):
        raise ValueError("missing pull request commit evidence")
    pull = snapshot["pull"]
    kind = owned_kind(pull, repo)
    head = pull["head"].get("sha")
    if not isinstance(head, str) or not SHA.fullmatch(head):
        raise ValueError("missing current pull request head")
    labels = pull.get("labels")
    if not isinstance(labels, list) or any(
        not isinstance(label, dict) or not isinstance(label.get("name"), str) for label in labels
    ):
        raise ValueError("missing pull request label evidence")
    held = HOLD_LABEL in {label["name"] for label in labels}
    label_trusted = trusted_hold_label(snapshot["hold_events"])
    holds, cancellation_pending = active_holds(snapshot["comments"], set(commits))
    ci = ci_evidence(snapshot["statuses"], snapshot["checks"], expected_head)
    blockers = []
    if pull.get("number") != number:
        blockers.append("pull request number does not match")
    if kind is None:
        blockers.append("pull request is not an owned robot upgrade or dependency PR")
    if pull.get("state") != "open" or pull.get("merged") is not False or pull.get("draft") is not False:
        blockers.append("pull request must be open, unmerged, and ready for review")
    if head != expected_head or head not in commits:
        blockers.append("current head does not match the expected, evidenced pull request head")
    if any(hold["kind"] != "retry-exhaustion" for hold in holds):
        blockers.append("active human or unexplained hold must not be cleared automatically")
    if held and not label_trusted:
        blockers.append("hold label has human, unknown, or inconsistent provenance")
    if held and not holds and not cancellation_pending:
        blockers.append("hold label has no active evidenced retry-exhaustion command")
    refresh_blockers = list(blockers)
    retry_blockers = list(blockers)
    if any(item["pending"] for item in ci["blocked"]):
        retry_blockers.append("current-head CI is pending")
    if any(item["state"] not in (
        "pending", "queued", "in_progress", "waiting",
        "failure", "error", "timed_out", "cancelled", "action_required", "startup_failure",
    ) for item in ci["blocked"]):
        retry_blockers.append("current-head CI contains unknown evidence")
    failures = [item for item in ci["blocked"] if item["state"] in (
        "failure", "error", "timed_out", "cancelled", "action_required", "startup_failure",
    ) and item["url"]]
    if not failures:
        retry_blockers.append("no current-head failing CI URL")
    marker = f"<!-- {RECOVERY_MARKER}{head} -->"
    if any(marker in comment["body"] and comment["user"]["login"] == ROBOT
           for comment in snapshot["comments"]):
        retry_blockers.append("single recovery budget for this head is already consumed")
    if kind != "upgrade":
        refresh_blockers.append("compiler refresh requires the owned upgrade PR identity")
    release_blockers = list(blockers)
    if held and holds:
        if ci["blocked"]:
            release_blockers.append("current head has failing, pending, or unknown non-Tide CI")
        if not ci["positive"]:
            release_blockers.append("current head has no positive non-Tide CI evidence")
        required = {"ci/pipeline-gate", "ci/prow/e2e-parallel"}
        if not required.issubset(ci["positive"]):
            release_blockers.append("required pipeline gate and E2E checks must succeed before automatic hold recovery")
    status = "blocked" if release_blockers else (
        "not-held" if not held else "cancel-pending" if cancellation_pending else "eligible"
    )
    return {
        "repository": repo,
        "pull_request": number,
        "expected_head": expected_head,
        "head": head,
        "kind": kind,
        "held": held,
        "active_holds": holds,
        "ci": ci,
        "release_status": status,
        "can_release_hold": status == "eligible",
        "can_recover_retry": held and bool(holds) and not cancellation_pending and not retry_blockers,
        "retry_blockers": retry_blockers,
        "recovery_marker": marker,
        "can_refresh_upgrade": not refresh_blockers,
        "release_blockers": release_blockers,
        "refresh_blockers": refresh_blockers,
    }


def execute(mode, repo, number, expected_head, evidence=None):
    """Re-fetch the complete authorization before each fixed-command mutation."""
    if mode not in ("assess", "release-hold", "verify-upgrade", "recover-retry", "release-regenerated"):
        raise ValueError("unsupported maintenance action")
    number = validate_target(repo, number, expected_head)
    result = None
    for _ in range(2 if mode not in ("assess", "verify-upgrade") else 1):
        snapshot = fetch_snapshot(repo, number, expected_head)
        result = assess(snapshot, repo, number, expected_head)
        if mode == "assess":
            return result
        if mode == "verify-upgrade":
            if result["refresh_blockers"]:
                raise ValueError("; ".join(result["refresh_blockers"]))
            return result
        if not result["held"] or result["release_status"] == "cancel-pending":
            return result
        if mode == "release-hold":
            if result["release_blockers"]:
                raise ValueError("; ".join(result["release_blockers"]))
            body = "/hold cancel"
        elif mode == "recover-retry":
            if result["retry_blockers"]:
                raise ValueError("; ".join(result["retry_blockers"]))
            if not isinstance(evidence, str) or len(evidence) < 40 or len(evidence) > 4000:
                raise ValueError("recovery requires concise diagnosed transient-failure evidence")
            if re.search(r"^\s*/", evidence, re.MULTILINE) or RECOVERY_MARKER in evidence:
                raise ValueError("recovery evidence cannot contain commands or markers")
            links = {url.rstrip(".;,") for url in re.findall(r"https://[^\s)<>]+", evidence)}
            failed = {item["url"] for item in result["ci"]["blocked"] if item["url"]}
            trusted = re.compile(
                r"https://(?:prow\.ci\.openshift\.org|gcsweb-ci\.apps\.ci\.l2s4\.p1\.openshiftapps\.com|"
                r"gcs\.ci\.openshift\.org|storage\.googleapis\.com|github\.com)/"
            )
            if not links.intersection(failed) or not any(
                trusted.match(url) for url in links.difference(failed)
            ):
                raise ValueError("recovery needs a current failure URL and trusted comparison run URL")
            body = f"{result['recovery_marker']}\n{evidence}\n\n/hold cancel\n/retest-required"
        else:
            if result["refresh_blockers"]:
                raise ValueError("; ".join(result["refresh_blockers"]))
            validate_target(repo, number, evidence)
            if evidence == expected_head or any(
                hold["sha"] == expected_head for hold in result["active_holds"]
            ):
                raise ValueError("regeneration must produce a genuinely new head")
            subprocess.run(["git", "merge-base", "--is-ancestor", evidence, expected_head], check=True)
            changed = subprocess.run(
                ["git", "diff", "--name-only", f"origin/{snapshot['pull']['base']['ref']}...{expected_head}"],
                check=True, capture_output=True, text=True,
            ).stdout.splitlines()
            if not changed or any(not path.startswith(".github/") for path in changed):
                raise ValueError("regeneration must change only compiler-owned .github content")
            if compiler_versions(evidence) == compiler_versions(expected_head):
                raise ValueError("regeneration must change the compiler release provenance")
            body = f"Validated compiler regeneration from {evidence} to {expected_head}.\n\n/hold cancel"
    response = subprocess.run(
        ["gh", "api", "--method", "POST", f"repos/{repo}/issues/{number}/comments", "--input", "-"],
        input=json.dumps({"body": body}), check=True, capture_output=True,
        text=True, env=os.environ.copy(),
    )
    comment = json.loads(response.stdout)
    if not isinstance(comment, dict) or type(comment.get("id")) is not int or comment.get("body") != body:
        raise ValueError("hold cancellation response is incomplete; inspect the PR before retrying")
    result["release_status"] = "cancel-requested"
    result["can_release_hold"] = False
    result["cancellation_comment_id"] = comment["id"]
    return result


def main(argv=None):
    argv = sys.argv[1:] if argv is None else argv
    try:
        if len(argv) == 2 and argv[0] == "compiler-versions":
            print(json.dumps(compiler_versions(argv[1])))
            return 0
        if len(argv) == 3 and argv[0] == "inspect":
            _, repo, number = argv
            # Validate paths before the first API request; the discovered head is not authorization.
            number = validate_target(repo, number, "0" * 40)
            pull = fetch_pull(repo, number)
            if not isinstance(pull.get("head"), dict):
                raise ValueError("missing current pull request head")
            result = execute("assess", repo, number, pull["head"].get("sha"))
        elif len(argv) in (4, 5):
            result = execute(*argv)
        else:
            raise ValueError("usage: agentic_pr_maintenance.py compiler-versions REF | inspect REPO PR | assess|release-hold|verify-upgrade REPO PR HEAD | recover-retry REPO PR HEAD EVIDENCE | release-regenerated REPO PR HEAD PREVIOUS_HEAD")
        print(json.dumps(result, sort_keys=True))
        return 0
    except (OSError, KeyError, TypeError, ValueError, subprocess.CalledProcessError) as error:
        print(f"Agentic PR maintenance refused action: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
