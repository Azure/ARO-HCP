#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


def load_json(path: Path) -> object:
    return json.loads(path.read_text(encoding="utf-8"))


def matches_type(value: object, expected_type: str) -> bool:
    if expected_type == "array":
        return isinstance(value, list)
    if expected_type == "boolean":
        return isinstance(value, bool)
    if expected_type == "integer":
        return isinstance(value, int) and not isinstance(value, bool)
    if expected_type == "null":
        return value is None
    if expected_type == "number":
        return isinstance(value, (int, float)) and not isinstance(value, bool)
    if expected_type == "object":
        return isinstance(value, dict)
    if expected_type == "string":
        return isinstance(value, str)
    raise ValueError(f"unsupported schema type: {expected_type}")


def validate_instance(value: object, schema: dict[str, object], path: str, errors: list[str]) -> None:
    expected_types = schema.get("type")
    allowed_types = expected_types if isinstance(expected_types, list) else [expected_types] if expected_types else []
    if allowed_types and not any(matches_type(value, expected_type) for expected_type in allowed_types):
        readable_types = ", ".join(str(item) for item in allowed_types)
        errors.append(f"{path} must be one of [{readable_types}]")
        return

    if isinstance(value, dict):
        properties = schema.get("properties", {})
        required = schema.get("required", [])
        additional_properties = schema.get("additionalProperties", True)

        if isinstance(required, list):
            for key in required:
                if key not in value:
                    errors.append(f"{path}.{key} is required")

        if additional_properties is False and isinstance(properties, dict):
            extra_keys = sorted(set(value) - set(properties))
            for key in extra_keys:
                errors.append(f"{path}.{key} is not allowed")

        if isinstance(properties, dict):
            for key, child_schema in properties.items():
                if key not in value or not isinstance(child_schema, dict):
                    continue
                validate_instance(value[key], child_schema, f"{path}.{key}", errors)
        return

    if isinstance(value, list):
        item_schema = schema.get("items")
        if isinstance(item_schema, dict):
            for index, item in enumerate(value):
                validate_instance(item, item_schema, f"{path}[{index}]", errors)


def validate_history_corpus(corpus: object, schema: dict[str, object], domain_ids: set[str]) -> list[str]:
    errors: list[str] = []
    validate_instance(corpus, schema, "$", errors)
    if not isinstance(corpus, dict):
        return errors

    metadata = corpus.get("metadata")
    pull_requests = corpus.get("pull_requests")
    if isinstance(metadata, dict):
        if metadata.get("since") is None and metadata.get("before") is None:
            errors.append("$.metadata must include at least one of since or before")

        pull_request_count = metadata.get("pull_request_count")
        if pull_request_count is not None:
            if not isinstance(pull_request_count, int) or isinstance(pull_request_count, bool):
                errors.append("$.metadata.pull_request_count must be an integer when present")
            elif isinstance(pull_requests, list) and pull_request_count != len(pull_requests):
                errors.append(
                    "$.metadata.pull_request_count does not match the number of pull_requests "
                    f"({pull_request_count} != {len(pull_requests)})"
                )

        paths = metadata.get("paths")
        if paths is not None:
            if not isinstance(paths, list):
                errors.append("$.metadata.paths must be an array when present")
            elif not all(isinstance(item, str) for item in paths):
                errors.append("$.metadata.paths must contain only strings")

    if isinstance(pull_requests, list):
        seen_numbers: set[int] = set()
        for index, pull_request in enumerate(pull_requests):
            if not isinstance(pull_request, dict):
                continue

            number = pull_request.get("number")
            if isinstance(number, int) and not isinstance(number, bool):
                if number in seen_numbers:
                    errors.append(f"$.pull_requests[{index}].number repeats pull request #{number}")
                seen_numbers.add(number)

            domains = pull_request.get("domains")
            if isinstance(domains, list):
                unknown_domains = sorted(
                    {domain for domain in domains if isinstance(domain, str)} - domain_ids
                )
                if unknown_domains:
                    errors.append(
                        f"$.pull_requests[{index}].domains contains unknown domain ids: {unknown_domains}"
                    )

    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate reviewer history corpus JSON against the schema.")
    parser.add_argument("paths", nargs="*", help="Optional history corpus JSON paths to validate")
    args = parser.parse_args()

    reviewer_root = Path(__file__).resolve().parents[2]
    schema_path = reviewer_root / "common/schema/history-corpus.schema.json"
    routing_path = reviewer_root / "common/domain-routing/path-routing.json"
    schema = load_json(schema_path)
    routing = load_json(routing_path)
    if not isinstance(schema, dict):
        print(f"{schema_path} must contain a JSON object", file=sys.stderr)
        return 1
    if not isinstance(routing, dict):
        print(f"{routing_path} must contain a JSON object", file=sys.stderr)
        return 1

    if args.paths:
        corpus_paths = [Path(item).expanduser() for item in args.paths]
    else:
        corpus_paths = [reviewer_root / "tests/history-corpus-smoke.json"]

    if not corpus_paths:
        print("no history corpus files found", file=sys.stderr)
        return 1

    domain_ids = {domain["id"] for domain in routing.get("domains", []) if isinstance(domain, dict) and "id" in domain}
    errors: list[str] = []
    for path in corpus_paths:
        try:
            corpus = load_json(path)
        except json.JSONDecodeError as exc:
            errors.append(f"{path} is not valid JSON: {exc}")
            continue

        for error in validate_history_corpus(corpus, schema, domain_ids):
            errors.append(f"{path}: {error}")

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print(f"Validated {len(corpus_paths)} history corpus snapshots against {schema_path.name}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
