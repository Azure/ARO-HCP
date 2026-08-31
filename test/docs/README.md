# Documentation Tests

This directory contains automated tests for validating README files and documentation.

## Overview

These tests ensure that:
- Commands documented in READMEs are syntactically correct
- Required tools and prerequisites are properly documented
- Internal links point to existing files
- Make targets mentioned in docs actually exist
- Code examples use current API versions

## Tests

### personal-dev-readme-test.sh

Validates the [personal-dev.md](../../docs/personal-dev.md) documentation.

**What it tests:**
- README file exists and has required sections
- Azure CLI prerequisites are documented (version >= 2.68.0)
- All documented make targets exist
- kubectl commands are valid
- PERSIST environment variable is documented
- Cleanup retention periods are correct (48h / 15 days)
- Resource group naming patterns are documented
- Azure tenant/subscription IDs are valid GUIDs
- Internal documentation links are not broken
- Required tools (az, kubectl) are available

**Running locally:**
```bash
# Dry-run mode (CI safe, no Azure access needed)
./test/docs/personal-dev-readme-test.sh

# Or via make
make test-docs
```

**Test modes:**
- `dry-run` (default): Validates syntax without executing commands
- `full`: Requires Azure access, tests actual connectivity

## CI Integration

Tests run automatically on PRs that modify:
- Any `*.md` files
- Test scripts in `test/docs/`
- CI workflow configurations

See [.github/workflows/docs-validation.yml](../../.github/workflows/docs-validation.yml)

## Adding New Tests

To test a new README:

1. Create a test script in `test/docs/`
2. Follow the pattern from `personal-dev-readme-test.sh`
3. Add to the CI workflow
4. Document in this README

### Test Script Template

```bash
#!/usr/bin/env bash
set -euo pipefail

README_FILE="/path/to/readme.md"
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

test_start() {
    TESTS_RUN=$((TESTS_RUN + 1))
    echo "[TEST ${TESTS_RUN}] $1"
}

test_pass() {
    TESTS_PASSED=$((TESTS_PASSED + 1))
    echo "✓ PASS"
}

test_fail() {
    TESTS_FAILED=$((TESTS_FAILED + 1))
    echo "✗ FAIL: $1"
}

# Your tests here
test_something() {
    test_start "Test description"
    if [[ condition ]]; then
        test_pass
    else
        test_fail "Reason"
    fi
}

# Summary
echo "Tests: ${TESTS_RUN} | Passed: ${TESTS_PASSED} | Failed: ${TESTS_FAILED}"
[[ ${TESTS_FAILED} -eq 0 ]] && exit 0 || exit 1
```

## Markdown Linting

Project uses `markdownlint` with configuration in `.markdownlint.json`.

**Run locally:**
```bash
# Install
npm install -g markdownlint-cli

# Lint all markdown
markdownlint '**/*.md'

# Auto-fix issues
markdownlint '**/*.md' --fix
```

## Link Checking

Broken links are detected using `markdown-link-check`.

Configuration: `.markdown-link-check.json`

## Troubleshooting

### Tests fail on macOS

Some GNU tools work differently on macOS. The test scripts are designed to be portable, but if you encounter issues:
```bash
brew install grep  # GNU grep
brew install gnu-sed  # GNU sed
```

### kubectl warnings during tests

This is expected when running without a kubeconfig. The tests validate command syntax, not execution.

### Make targets not found

Ensure you're running from the repository root:
```bash
cd /path/to/ARO-HCP
./test/docs/personal-dev-readme-test.sh
```
