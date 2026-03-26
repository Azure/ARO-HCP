#!/usr/bin/env python3
from __future__ import annotations

import argparse
import classify_paths
import json
import re
import subprocess
import sys
from datetime import datetime, timezone
from pathlib import Path

from validate_history_corpus import load_json as load_json_file
from validate_history_corpus import validate_history_corpus

MERGE_PR_RE = re.compile(r'Merge pull request #(\d+)')

def run(cmd: list[str], *, cwd: Path | None = None) -> str:
    result = subprocess.run(cmd, cwd=cwd, text=True, capture_output=True)
    if result.returncode != 0:
        raise RuntimeError(f"command failed: {' '.join(cmd)}\n{result.stderr.strip()}")
    return result.stdout

def repo_root(explicit_root: str | None) -> Path:
    if explicit_root:
        root = Path(explicit_root).expanduser().resolve()
        if not root.exists():
            raise RuntimeError(f'repo root does not exist: {root}')
        return root
    return Path(run(['git', 'rev-parse', '--show-toplevel']).strip())

def load_routing() -> dict:
    return classify_paths.load_routing()

def classify(paths: list[str], routing: dict) -> list[str]:
    classified = classify_paths.classify(paths, routing)
    return [domain['domain'] for domain in classified['domains']]

def merged_pr_numbers(
    root: Path,
    since: str | None,
    before: str | None,
    limit: int | None,
    git_ref: str,
    paths: list[str],
) -> list[int]:
    cmd = ['git', 'log', git_ref]
    if since:
        cmd.append(f'--since={since}')
    if before:
        cmd.append(f'--before={before}')
    cmd.extend(['--merges', '--first-parent', '--format=%s'])
    if paths:
        cmd.append('--')
        cmd.extend(paths)
    log = run(cmd, cwd=root)
    numbers = []
    seen = set()
    for line in log.splitlines():
        match = MERGE_PR_RE.search(line)
        if not match:
            continue
        number = int(match.group(1))
        if number in seen:
            continue
        seen.add(number)
        numbers.append(number)
        if limit and len(numbers) >= limit:
            break
    return numbers

def gh_api(endpoint: str) -> object:
    return json.loads(run(['gh', 'api', endpoint]))

def paginated(endpoint: str) -> list[object]:
    page = 1
    items: list[object] = []
    while True:
        batch = gh_api(f'{endpoint}{"&" if "?" in endpoint else "?"}per_page=100&page={page}')
        if not batch:
            break
        if not isinstance(batch, list):
            raise RuntimeError(f'expected list response for {endpoint}')
        items.extend(batch)
        if len(batch) < 100:
            break
        page += 1
    return items

def pull_request_bundle(repo: str, number: int, include_check_runs: bool, routing: dict) -> dict:
    pull = gh_api(f'/repos/{repo}/pulls/{number}')
    files = paginated(f'/repos/{repo}/pulls/{number}/files')
    commits = paginated(f'/repos/{repo}/pulls/{number}/commits')
    reviews = paginated(f'/repos/{repo}/pulls/{number}/reviews')
    review_comments = paginated(f'/repos/{repo}/pulls/{number}/comments')
    issue_comments = paginated(f'/repos/{repo}/issues/{number}/comments')
    check_runs = []
    if include_check_runs:
        sha = pull['head']['sha']
        response = gh_api(f'/repos/{repo}/commits/{sha}/check-runs?per_page=100')
        check_runs = response.get('check_runs', [])
    changed_paths = [item['filename'] for item in files]
    return {
        'number': number,
        'title': pull['title'],
        'body': pull.get('body'),
        'merged_at': pull['merged_at'],
        'html_url': pull['html_url'],
        'author': pull['user']['login'],
        'merged_by': pull.get('merged_by', {}).get('login') if pull.get('merged_by') else None,
        'labels': [label['name'] for label in pull.get('labels', [])],
        'changed_files': files,
        'commits': commits,
        'reviews': reviews,
        'review_comments': review_comments,
        'issue_comments': issue_comments,
        'check_runs': check_runs,
        'domains': classify(changed_paths, routing),
    }

def main() -> int:
    parser = argparse.ArgumentParser(description='Bootstrap ARO-HCP PR review history from local git merges and GitHub metadata.')
    parser.add_argument('--repo', default='Azure/ARO-HCP', help='GitHub repo in OWNER/NAME form')
    parser.add_argument('--repo-root', default=None, help='Path to the local git checkout to mine. Defaults to the current git repo.')
    parser.add_argument('--git-ref', default='origin/main', help='Git ref to scan for merged PRs. Defaults to origin/main.')
    parser.add_argument('--since', default=None, help='Date for git log --since, for example 2025-09-25')
    parser.add_argument('--before', default=None, help='Date for git log --before, for example 2025-09-25')
    parser.add_argument('--limit', type=int, default=None, help='Maximum number of merged PRs to fetch')
    parser.add_argument('--output', required=True, help='Output JSON path')
    parser.add_argument('--paths', nargs='*', default=[], help='Optional path prefixes to limit merge discovery to')
    parser.add_argument('--include-check-runs', action='store_true', help='Also fetch check runs for each PR head SHA')
    parser.add_argument(
        '--include-repo-root',
        action='store_true',
        help='Include the local checkout path in generated artifacts. Off by default to keep checked-in metadata portable.',
    )
    parser.add_argument('--state-file', default=None, help='Optional path to write refresh state metadata')
    args = parser.parse_args()

    if not args.since and not args.before:
        parser.error('at least one of --since or --before is required')

    root = repo_root(args.repo_root)
    routing = load_routing()
    numbers = merged_pr_numbers(root, args.since, args.before, args.limit, args.git_ref, args.paths)
    if not numbers:
        window = []
        if args.since:
            window.append(f'since {args.since}')
        if args.before:
            window.append(f'before {args.before}')
        print(
            f"warning: no merged pull requests found in {root} on {args.git_ref}"
            + (f" ({', '.join(window)})" if window else ''),
            file=sys.stderr,
        )
    metadata = {
        'repo': args.repo,
        'git_ref': args.git_ref,
        'since': args.since,
        'before': args.before,
        'paths': args.paths,
        'generated_at': datetime.now(timezone.utc).isoformat(),
        'source': f'git first-parent merge history on {args.git_ref} + gh api pull request metadata',
        'reviewer_version': 'v1-alpha',
        'pull_request_count': len(numbers),
    }
    if args.include_repo_root:
        metadata['repo_root'] = str(root)

    corpus = {
        'metadata': metadata,
        'pull_requests': [
            pull_request_bundle(args.repo, number, args.include_check_runs, routing)
            for number in numbers
        ],
    }

    schema = load_json_file(Path(__file__).resolve().parents[1] / 'schema' / 'history-corpus.schema.json')
    history_validation_errors = validate_history_corpus(
        corpus,
        schema,
        {domain['id'] for domain in routing['domains']},
    )
    if history_validation_errors:
        raise RuntimeError('generated history corpus failed validation:\n' + '\n'.join(history_validation_errors))

    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(json.dumps(corpus, indent=2, sort_keys=True) + '\n', encoding='utf-8')

    if args.state_file:
        state = {
            'repo': args.repo,
            'git_ref': args.git_ref,
            'since': args.since,
            'before': args.before,
            'paths': args.paths,
            'generated_at': corpus['metadata']['generated_at'],
            'pull_request_count': len(numbers),
            'last_pull_request': numbers[0] if numbers else None,
        }
        if args.include_repo_root:
            state['repo_root'] = str(root)
        Path(args.state_file).write_text(json.dumps(state, indent=2, sort_keys=True) + '\n', encoding='utf-8')

    return 0

if __name__ == '__main__':
    raise SystemExit(main())
