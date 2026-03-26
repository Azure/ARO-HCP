PYTHON ?= python3
REVIEWER_DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
REPO_ROOT ?= $(abspath $(REVIEWER_DIR)/../..)
GIT_REF ?= origin/main
SINCE ?= 2025-09-25
OUTPUT ?= fixtures/history/bootstrap-$(SINCE).json
PATHS ?=
SELECTION ?= mixed
EVAL_CLAUDE_BIN ?= claude
EVAL_REVIEW_MODEL ?= claude-sonnet-4-6
EVAL_JUDGE_MODEL ?= claude-sonnet-4-6
EVAL_REVIEW_TIMEOUT ?= 900
EVAL_JUDGE_TIMEOUT ?= 300

.PHONY: pycheck jsoncheck inventorycheck historycheck commandcheck copilotcheck fixturecheck classify-test evalrunner-test validate evalcheck classify bootstrap-history

pycheck:
	PYTHONDONTWRITEBYTECODE=1 $(PYTHON) -m py_compile common/tools/classify_paths.py common/tools/bootstrap_history.py common/tools/run_reviewer_evals.py common/tools/test_classify_paths.py common/tools/test_run_reviewer_evals.py common/tools/validate_asset_inventory.py common/tools/validate_claude_commands.py common/tools/validate_copilot_instructions.py common/tools/validate_fixture_quality.py common/tools/validate_history_corpus.py common/tools/validate_reviewer_assets.py

jsoncheck:
	$(PYTHON) common/tools/validate_reviewer_assets.py

inventorycheck:
	$(PYTHON) common/tools/validate_asset_inventory.py

historycheck:
	$(PYTHON) common/tools/validate_history_corpus.py

commandcheck:
	$(PYTHON) common/tools/validate_claude_commands.py

copilotcheck:
	$(PYTHON) common/tools/validate_copilot_instructions.py

fixturecheck:
	$(PYTHON) common/tools/validate_fixture_quality.py

classify-test:
	$(PYTHON) common/tools/test_classify_paths.py

evalrunner-test:
	$(PYTHON) common/tools/test_run_reviewer_evals.py

validate: pycheck jsoncheck inventorycheck historycheck commandcheck copilotcheck fixturecheck classify-test evalrunner-test

evalcheck:
	$(PYTHON) common/tools/run_reviewer_evals.py --selection "$(SELECTION)" --claude-bin "$(EVAL_CLAUDE_BIN)" --review-model "$(EVAL_REVIEW_MODEL)" --judge-model "$(EVAL_JUDGE_MODEL)" --review-timeout $(EVAL_REVIEW_TIMEOUT) --judge-timeout $(EVAL_JUDGE_TIMEOUT)

classify:
	$(PYTHON) common/tools/classify_paths.py --pretty $(PATHS)

bootstrap-history:
	$(PYTHON) common/tools/bootstrap_history.py --repo-root "$(REPO_ROOT)" --git-ref "$(GIT_REF)" --since "$(SINCE)" --output "$(OUTPUT)"
