#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


def load_routing() -> dict:
    routing_path = Path(__file__).resolve().parents[1] / 'domain-routing' / 'path-routing.json'
    return json.loads(routing_path.read_text())


def normalize_path(raw_path: str) -> str:
    path = raw_path.strip().replace('\\', '/')
    while path.startswith('./'):
        path = path[2:]
    path = path.lstrip('/')
    while '//' in path:
        path = path.replace('//', '/')
    return path.rstrip('/') if path != '/' else path


def matches_prefix(path: str, prefix: str) -> bool:
    normalized_path = normalize_path(path)
    normalized_prefix = normalize_path(prefix)
    if not normalized_path or not normalized_prefix:
        return False
    return normalized_path == normalized_prefix or normalized_path.startswith(f'{normalized_prefix}/')


def iter_paths(args: argparse.Namespace) -> list[str]:
    paths = [normalize_path(path) for path in args.paths if path.strip()]
    if args.stdin or (not paths and not sys.stdin.isatty()):
        paths.extend(normalize_path(line) for line in sys.stdin if line.strip())
    return [path for path in paths if path]


def classify(paths: list[str], routing: dict) -> dict:
    domain_hits: dict[str, dict] = {}
    unmatched: list[str] = []
    for path in paths:
        matched = False
        for domain in routing['domains']:
            prefixes = [prefix for prefix in domain['path_prefixes'] if matches_prefix(path, prefix)]
            if not prefixes:
                continue
            matched = True
            bucket = domain_hits.setdefault(
                domain['id'],
                {
                    'domain': domain['id'],
                    'display_name': domain['display_name'],
                    'priority': domain['priority'],
                    'sub_reviewer': domain['sub_reviewer'],
                    'history_fixtures': domain.get('history_fixtures', []),
                    'owners_files': domain['owners_files'],
                    'paths': [],
                    'high_risk_paths': [],
                },
            )
            bucket['paths'].append(path)
            if any(matches_prefix(path, prefix) for prefix in domain.get('high_risk_prefixes', [])):
                bucket['high_risk_paths'].append(path)
        if not matched:
            unmatched.append(path)

    ordered = sorted(domain_hits.values(), key=lambda item: (item['priority'], item['display_name']))
    for item in ordered:
        item['paths'] = sorted(set(item['paths']))
        item['high_risk_paths'] = sorted(set(item['high_risk_paths']))
    return {
        'domains': ordered,
        'always_load': routing.get('always_load', []),
        'unmatched_paths': sorted(set(unmatched)),
    }

def main() -> int:
    parser = argparse.ArgumentParser(description='Classify ARO-HCP paths into reviewer domains.')
    parser.add_argument('paths', nargs='*', help='Paths to classify')
    parser.add_argument('--stdin', action='store_true', help='Read newline-delimited paths from stdin')
    parser.add_argument('--pretty', action='store_true', help='Pretty-print JSON output')
    args = parser.parse_args()

    paths = iter_paths(args)
    if not paths:
        parser.error('provide at least one path or use --stdin')

    result = classify(paths, load_routing())
    if args.pretty:
        print(json.dumps(result, indent=2, sort_keys=True))
    else:
        print(json.dumps(result, separators=(',', ':')))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
