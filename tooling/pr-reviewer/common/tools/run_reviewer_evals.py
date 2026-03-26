#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import textwrap
from dataclasses import dataclass
from pathlib import Path


DEFAULT_MIXED_IDS = [9, 10, 11, 12, 13]
DEFAULT_REVIEW_MODEL = "claude-sonnet-4-6"
DEFAULT_JUDGE_MODEL = "claude-sonnet-4-6"
PASSING_SCORE = 4
# Headless evals auto-approve only these read-only tools via --permission-mode bypassPermissions.
# Do not add write or execute tools here without an explicit security review.
REVIEW_ALLOWED_TOOLS = "Read,Glob,Grep"

JUDGE_SCHEMA = {
    "type": "object",
    "properties": {
        "score": {"type": "integer", "minimum": 0, "maximum": 5},
        "summary": {"type": "string"},
        "strengths": {"type": "array", "items": {"type": "string"}},
        "missing_behaviors": {"type": "array", "items": {"type": "string"}},
        "spurious_behaviors": {"type": "array", "items": {"type": "string"}},
    },
    "required": ["score", "summary", "strengths", "missing_behaviors", "spurious_behaviors"],
    "additionalProperties": False,
}


@dataclass(frozen=True)
class EvalCase:
    id: int
    prompt: str
    expected_output: str
    files: list[str]
    domains: list[str]


@dataclass(frozen=True)
class EvalResult:
    eval_case: EvalCase
    score: int
    summary: str
    strengths: list[str]
    missing_behaviors: list[str]
    spurious_behaviors: list[str]
    review_output: str
    review_cost_usd: float
    judge_cost_usd: float

    @property
    def passed(self) -> bool:
        return self.score >= PASSING_SCORE


def load_evals(path: Path) -> list[EvalCase]:
    payload = json.loads(path.read_text(encoding="utf-8"))
    return [
        EvalCase(
            id=entry["id"],
            prompt=entry["prompt"],
            expected_output=entry["expected_output"],
            files=entry["files"],
            domains=entry["domains"],
        )
        for entry in payload["evals"]
    ]


def parse_selection(selection: str | None, evals: list[EvalCase]) -> list[EvalCase]:
    eval_by_id = {entry.id: entry for entry in evals}

    if selection is None or not selection.strip() or selection.strip() == "mixed":
        missing = [eval_id for eval_id in DEFAULT_MIXED_IDS if eval_id not in eval_by_id]
        if missing:
            raise ValueError(f"mixed suite ids missing from evals.json: {missing}")
        return [eval_by_id[eval_id] for eval_id in DEFAULT_MIXED_IDS]

    if selection.strip() == "all":
        return sorted(evals, key=lambda entry: entry.id)

    selected_ids: list[int] = []
    for part in selection.split(","):
        value = part.strip()
        if not value:
            continue
        if not value.isdigit():
            raise ValueError(f"invalid eval selection token: {value!r}")
        selected_ids.append(int(value))

    if not selected_ids:
        raise ValueError("no eval ids were selected")

    unknown = [eval_id for eval_id in selected_ids if eval_id not in eval_by_id]
    if unknown:
        raise ValueError(f"unknown eval ids: {unknown}")

    return [eval_by_id[eval_id] for eval_id in selected_ids]


def build_review_prompt(eval_case: EvalCase) -> str:
    supporting_files = "\n".join(f"- {path}" for path in eval_case.files)
    domains = ", ".join(eval_case.domains)
    return textwrap.dedent(
        f"""
        Use the in-repo ARO-HCP reviewer under `tooling/pr-reviewer/` to review the following synthetic change scenario.

        Requirements:
        - Work from `tooling/pr-reviewer/SKILL.md` and `tooling/pr-reviewer/MANIFEST.md`.
        - Read the supporting reviewer assets below if they help sharpen the review.
        - Do not modify files.
        - Do not mention eval mechanics in the answer.
        - Return only the review you would give a teammate.

        Eval metadata:
        - eval id: {eval_case.id}
        - routed domains: {domains}

        Supporting reviewer assets:
        {supporting_files}

        Eval scenario:
        {eval_case.prompt}
        """
    ).strip()


def build_judge_prompt(eval_case: EvalCase, review_output: str) -> str:
    supporting_files = "\n".join(f"- {path}" for path in eval_case.files)
    return textwrap.dedent(
        f"""
        Grade an automated ARO-HCP reviewer eval result.

        Use this rubric:
        - 5 = strong pass: the review materially covers the expected behavior with no major noise
        - 4 = pass: the important behavior is present but there are small omissions
        - 3 = borderline fail: some important expected behavior is missing or too weak
        - 2 = fail: major expected behavior is missing
        - 1 = poor fail: mostly wrong or shallow
        - 0 = unusable

        Expected behavior is about substance, not wording. Penalize hallucinated concerns, style-only noise, or failure to cover the main expected reviewer behavior.

        Eval id: {eval_case.id}
        Routed domains: {", ".join(eval_case.domains)}

        Supporting reviewer assets:
        {supporting_files}

        Eval scenario:
        {eval_case.prompt}

        Expected reviewer behavior:
        {eval_case.expected_output}

        Actual reviewer output:
        {review_output}
        """
    ).strip()


def run_claude_json(
    *,
    claude_bin: str,
    cwd: Path,
    prompt: str,
    model: str,
    timeout: int,
    allowed_tools: str | None = None,
    json_schema: dict[str, object] | None = None,
) -> dict[str, object]:
    command = [
        claude_bin,
        "--bare",
        "--no-session-persistence",
        "-p",
        "--output-format",
        "json",
        "--model",
        model,
        prompt,
    ]

    if allowed_tools:
        command[1:1] = ["--permission-mode", "bypassPermissions", "--allowedTools", allowed_tools]

    if json_schema is not None:
        command[1:1] = ["--json-schema", json.dumps(json_schema, separators=(",", ":"))]

    try:
        completed = subprocess.run(
            command,
            cwd=cwd,
            text=True,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
    except FileNotFoundError as exc:
        raise RuntimeError(f"failed to invoke {claude_bin!r}: {exc}") from exc
    except subprocess.TimeoutExpired as exc:
        raise RuntimeError(f"{claude_bin!r} timed out after {timeout}s") from exc

    if completed.returncode != 0:
        stderr = completed.stderr.strip() or completed.stdout.strip()
        raise RuntimeError(f"{claude_bin!r} exited with {completed.returncode}: {stderr}")

    try:
        payload = json.loads(completed.stdout)
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"{claude_bin!r} returned non-JSON output: {completed.stdout[:500]!r}") from exc

    if payload.get("is_error"):
        raise RuntimeError(f"{claude_bin!r} reported an error: {payload}")

    return payload


def run_eval_case(
    *,
    eval_case: EvalCase,
    repo_root: Path,
    claude_bin: str,
    review_model: str,
    judge_model: str,
    review_timeout: int,
    judge_timeout: int,
) -> EvalResult:
    review_payload = run_claude_json(
        claude_bin=claude_bin,
        cwd=repo_root,
        prompt=build_review_prompt(eval_case),
        model=review_model,
        timeout=review_timeout,
        allowed_tools=REVIEW_ALLOWED_TOOLS,
    )
    review_output = str(review_payload.get("result", "")).strip()
    if not review_output:
        raise RuntimeError(f"eval {eval_case.id} produced empty reviewer output")

    judge_payload = run_claude_json(
        claude_bin=claude_bin,
        cwd=repo_root,
        prompt=build_judge_prompt(eval_case, review_output),
        model=judge_model,
        timeout=judge_timeout,
        json_schema=JUDGE_SCHEMA,
    )
    structured = judge_payload.get("structured_output")
    if not isinstance(structured, dict):
        raise RuntimeError(f"eval {eval_case.id} judge output missing structured_output: {judge_payload}")

    return EvalResult(
        eval_case=eval_case,
        score=int(structured["score"]),
        summary=str(structured["summary"]),
        strengths=[str(item) for item in structured["strengths"]],
        missing_behaviors=[str(item) for item in structured["missing_behaviors"]],
        spurious_behaviors=[str(item) for item in structured["spurious_behaviors"]],
        review_output=review_output,
        review_cost_usd=float(review_payload.get("total_cost_usd", 0.0)),
        judge_cost_usd=float(judge_payload.get("total_cost_usd", 0.0)),
    )


def render_text(results: list[EvalResult], failures: list[str]) -> str:
    lines: list[str] = []
    if failures:
        lines.append("Runner errors:")
        for failure in failures:
            lines.append(f"- {failure}")
        lines.append("")

    for result in results:
        status = "PASS" if result.passed else "FAIL"
        lines.append(f"[{status}] eval {result.eval_case.id} score={result.score}/5")
        lines.append(f"summary: {result.summary}")
        if result.strengths:
            lines.append("strengths:")
            lines.extend(f"- {item}" for item in result.strengths)
        if result.missing_behaviors:
            lines.append("missing behaviors:")
            lines.extend(f"- {item}" for item in result.missing_behaviors)
        if result.spurious_behaviors:
            lines.append("spurious behaviors:")
            lines.extend(f"- {item}" for item in result.spurious_behaviors)
        if not result.passed:
            lines.append("review output:")
            lines.extend(f"  {line}" for line in result.review_output.splitlines())
        lines.append(
            f"cost_usd: review={result.review_cost_usd:.4f} judge={result.judge_cost_usd:.4f} total={result.review_cost_usd + result.judge_cost_usd:.4f}"
        )
        lines.append("")

    passed = sum(1 for result in results if result.passed)
    total_cost = sum(result.review_cost_usd + result.judge_cost_usd for result in results)
    lines.append(
        f"overall: {passed}/{len(results)} evals passed"
        + (f", {len(failures)} runner error(s)" if failures else "")
        + f", total_cost_usd={total_cost:.4f}"
    )
    return "\n".join(lines).strip()


def render_json(results: list[EvalResult], failures: list[str]) -> str:
    payload = {
        "passed": not failures and all(result.passed for result in results),
        "results": [
            {
                "id": result.eval_case.id,
                "score": result.score,
                "passed": result.passed,
                "summary": result.summary,
                "strengths": result.strengths,
                "missing_behaviors": result.missing_behaviors,
                "spurious_behaviors": result.spurious_behaviors,
                "review_output": result.review_output,
                "review_cost_usd": result.review_cost_usd,
                "judge_cost_usd": result.judge_cost_usd,
            }
            for result in results
        ],
        "runner_errors": failures,
        "total_cost_usd": sum(result.review_cost_usd + result.judge_cost_usd for result in results),
    }
    return json.dumps(payload, indent=2, sort_keys=True)


def main() -> int:
    parser = argparse.ArgumentParser(description="Run automated ARO-HCP reviewer evals with a headless reviewer and judge.")
    parser.add_argument(
        "--selection",
        default="mixed",
        help="Eval selection: mixed, all, or comma-separated eval ids (default: mixed)",
    )
    parser.add_argument("--claude-bin", default="claude", help="Path to the claude CLI binary")
    parser.add_argument("--review-model", default=DEFAULT_REVIEW_MODEL, help="Model to use for reviewer execution")
    parser.add_argument("--judge-model", default=DEFAULT_JUDGE_MODEL, help="Model to use for judging output")
    parser.add_argument("--review-timeout", type=int, default=900, help="Timeout in seconds for each reviewer run")
    parser.add_argument("--judge-timeout", type=int, default=300, help="Timeout in seconds for each judge run")
    parser.add_argument(
        "--output-format",
        choices=["text", "json"],
        default="text",
        help="Render human text or machine-readable JSON",
    )
    args = parser.parse_args()

    reviewer_root = Path(__file__).resolve().parents[2]
    repo_root = reviewer_root.parent.parent
    evals = load_evals(reviewer_root / "evals/evals.json")

    try:
        selected = parse_selection(args.selection, evals)
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 2

    results: list[EvalResult] = []
    failures: list[str] = []
    for eval_case in selected:
        try:
            results.append(
                run_eval_case(
                    eval_case=eval_case,
                    repo_root=repo_root,
                    claude_bin=args.claude_bin,
                    review_model=args.review_model,
                    judge_model=args.judge_model,
                    review_timeout=args.review_timeout,
                    judge_timeout=args.judge_timeout,
                )
            )
        except RuntimeError as exc:
            failures.append(f"eval {eval_case.id}: {exc}")

    output = render_json(results, failures) if args.output_format == "json" else render_text(results, failures)
    print(output)
    return 0 if not failures and all(result.passed for result in results) else 1


if __name__ == "__main__":
    raise SystemExit(main())
