<!-- Link to Jira issue -->

### What

<!-- Briefly describe what this PR does -->

### Why

<!-- Briefly explain why this change is needed -->

### Testing

Testing is required for feature completion and tests should be part of the pull
request along with the feature changes.

Describe the testing provided. If you did not add tests, provide a clear
justification.

- Unit tests
- [Integration tests](https://github.com/Azure/ARO-HCP/tree/main/test-integration)
- [E2E tests](https://github.com/Azure/ARO-HCP/blob/main/test/e2e/README.md)

### Special notes for your reviewer

<!-- optional -->

### PR Checklist

- [ ] PR is scoped to a single task (no mixed concerns)
- [ ] Title follows [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) format
- [ ] Summary explains the "Why" behind the change
- [ ] Linked to relevant ticket/issue
- [ ] Screenshots included (if graph/UI/metrics changes)
- [ ] Self-reviewed the diff
- [ ] CI/CD checks are passing (ignore Tide)
- [ ] Draft PR used for WIP (if applicable)
- [ ] Commit history is clean (rebased/squashed)
- [ ] Tricky code blocks are commented
- [ ] Specific reviewers tagged
- [ ] All comment threads resolved before merge

If E2E tests are included:

- [ ] E2E tests follow [Principles of Good E2E Test Case Design](https://github.com/Azure/ARO-HCP/blob/main/test/AGENTS.md)
- [ ] If new E2E use case is covered (via a new test or new check/verifier),
  demonstrate that the test is able to detect a defect/error and fail with
  proper error message and logs which communicates nature of the problem.
