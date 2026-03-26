#!/usr/bin/env python3
from __future__ import annotations

import json
import unittest

import run_reviewer_evals as runner


class RunReviewerEvalsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.evals = [
            runner.EvalCase(id=1, prompt="p1", expected_output="e1", files=["f1"], domains=["d1"]),
            runner.EvalCase(id=9, prompt="p9", expected_output="e9", files=["f9"], domains=["d9"]),
            runner.EvalCase(id=10, prompt="p10", expected_output="e10", files=["f10"], domains=["d10"]),
            runner.EvalCase(id=11, prompt="p11", expected_output="e11", files=["f11"], domains=["d11"]),
            runner.EvalCase(id=12, prompt="p12", expected_output="e12", files=["f12"], domains=["d12"]),
            runner.EvalCase(id=13, prompt="p13", expected_output="e13", files=["f13"], domains=["d13"]),
        ]

    def test_parse_selection_mixed_uses_default_suite(self) -> None:
        selected = runner.parse_selection("mixed", self.evals)
        self.assertEqual([9, 10, 11, 12, 13], [entry.id for entry in selected])

    def test_parse_selection_all_returns_sorted_evals(self) -> None:
        selected = runner.parse_selection("all", list(reversed(self.evals)))
        self.assertEqual([1, 9, 10, 11, 12, 13], [entry.id for entry in selected])

    def test_parse_selection_csv_ids(self) -> None:
        selected = runner.parse_selection("13,9,1", self.evals)
        self.assertEqual([13, 9, 1], [entry.id for entry in selected])

    def test_parse_selection_rejects_unknown_eval(self) -> None:
        with self.assertRaisesRegex(ValueError, "unknown eval ids"):
            runner.parse_selection("42", self.evals)

    def test_render_json_marks_pass_and_total_cost(self) -> None:
        result = runner.EvalResult(
            eval_case=self.evals[0],
            score=4,
            summary="strong enough",
            strengths=["covered core behavior"],
            missing_behaviors=[],
            spurious_behaviors=[],
            review_output="review",
            review_cost_usd=0.1,
            judge_cost_usd=0.2,
        )
        payload = json.loads(runner.render_json([result], []))
        self.assertTrue(payload["passed"])
        self.assertAlmostEqual(0.3, payload["total_cost_usd"])
        self.assertTrue(payload["results"][0]["passed"])


if __name__ == "__main__":
    unittest.main()
