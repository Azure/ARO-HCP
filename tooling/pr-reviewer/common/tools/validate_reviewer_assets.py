#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
from pathlib import Path


def load_json(path: Path) -> object:
    return json.loads(path.read_text())


def main() -> int:
    reviewer_root = Path(__file__).resolve().parents[2]
    repo_root = reviewer_root.parent.parent
    errors: list[str] = []

    def require(condition: bool, message: str) -> None:
        if not condition:
            errors.append(message)

    def require_file(path: Path, label: str) -> None:
        require(path.exists(), f"{label} missing: {path}")

    routing = load_json(reviewer_root / "common/domain-routing/path-routing.json")
    coverage = load_json(reviewer_root / "common/coverage/seed-coverage.json")
    staleness = load_json(reviewer_root / "common/staleness/seed-status.json")
    owners = load_json(reviewer_root / "common/owners/domain-owners.json")
    evals = load_json(reviewer_root / "evals/evals.json")

    version = load_json(reviewer_root / "common/versioning/version.json")
    load_json(reviewer_root / "common/versioning/asset-inventory.json")
    load_json(reviewer_root / "common/taxonomy/finding-types.json")
    load_json(reviewer_root / "common/suppressions/suppressions.json")
    load_json(reviewer_root / "common/schema/history-corpus.schema.json")

    domains = routing["domains"]
    routing_ids = [domain["id"] for domain in domains]
    require(len(routing_ids) == len(set(routing_ids)), "routing contains duplicate domain ids")

    for rel in routing.get("always_load", []):
        require_file(reviewer_root / rel, f"always_load asset {rel}")

    for domain in domains:
        require_file(reviewer_root / domain["sub_reviewer"], f"sub-reviewer for {domain['id']}")
        for rel in domain.get("history_fixtures", []):
            require_file(reviewer_root / rel, f"history fixture for {domain['id']} ({rel})")
        for rel in domain.get("owners_files", []):
            require_file(repo_root / rel, f"owners file for {domain['id']} ({rel})")

    coverage_ids = set(coverage["fixtures"].keys()) | set(coverage["eval_prompts"].keys())
    staleness_ids = {domain["id"] for domain in staleness["domains"]}
    owner_ids = {domain["domain"] for domain in owners["domains"]}
    routing_id_set = set(routing_ids)

    require(coverage_ids == routing_id_set, f"coverage domain ids differ from routing ids: {sorted(coverage_ids ^ routing_id_set)}")
    require(staleness_ids == routing_id_set, f"staleness domain ids differ from routing ids: {sorted(staleness_ids ^ routing_id_set)}")
    require(owner_ids == routing_id_set, f"owner domain ids differ from routing ids: {sorted(owner_ids ^ routing_id_set)}")

    for domain in domains:
        fixture_count = len(domain.get("history_fixtures", []))
        recorded_fixture_count = coverage["fixtures"].get(domain["id"])
        require(
            recorded_fixture_count is not None and recorded_fixture_count >= fixture_count,
            f"coverage fixture count for {domain['id']} is smaller than router fixture coverage: router={fixture_count}, recorded={recorded_fixture_count}",
        )

    eval_ids = [entry["id"] for entry in evals["evals"]]
    require(len(eval_ids) == len(set(eval_ids)), "evals contain duplicate ids")
    require(sorted(eval_ids) == eval_ids, "eval ids are not sorted")
    eval_coverage_counts = {domain_id: 0 for domain_id in routing_ids}

    for entry in evals["evals"]:
        require(bool(str(entry.get("prompt", "")).strip()), f"eval id {entry['id']} has empty prompt")
        require(bool(str(entry.get("expected_output", "")).strip()), f"eval id {entry['id']} has empty expected_output")
        require(bool(entry.get("files")), f"eval id {entry['id']} has no referenced files")
        require(isinstance(entry.get("domains"), list) and entry["domains"], f"eval id {entry['id']} has no domains")
        if isinstance(entry.get("domains"), list):
            require(
                len(entry["domains"]) == len(set(entry["domains"])),
                f"eval id {entry['id']} has duplicate domains",
            )
            unknown_domains = sorted(set(entry["domains"]) - routing_id_set)
            require(
                not unknown_domains,
                f"eval id {entry['id']} references unknown domains: {unknown_domains}",
            )
            for domain_id in entry["domains"]:
                if domain_id in eval_coverage_counts:
                    eval_coverage_counts[domain_id] += 1
        for rel in entry.get("files", []):
            require_file(repo_root / rel, f"eval file for id {entry['id']} ({rel})")

    for domain_id in routing_ids:
        require(
            coverage["eval_prompts"].get(domain_id) == eval_coverage_counts[domain_id],
            "coverage eval count for "
            f"{domain_id} does not match eval definitions: recorded={coverage['eval_prompts'].get(domain_id)}, "
            f"actual={eval_coverage_counts[domain_id]}",
        )

    require("version" in version and "status" in version and "last_updated" in version, "version.json missing required fields")

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print(f"Validated {len(domains)} domains, {len(evals['evals'])} evals, and reviewer asset references.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
