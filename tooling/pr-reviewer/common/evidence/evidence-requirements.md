# Evidence Requirements

Every finding should name:

- the relevant path or path group
- line/range when available
- the violated invariant, domain rule, or historical lesson
- the operational consequence
- the relevant validation command and signal when command output or generated drift is part of the evidence

Good evidence examples:

- `config/config.yaml` changed but no paired `config/rendered/**` update is present, which violates the repo rendering rule.
- `internal/validation/validate_nodepools.go` tightened version parsing, but no evidence shows old persisted values remain updatable.
- `make verify-generate` rewrote `internal/api/zz_generated.deepcopy.go`, but the PR did not include the corresponding generated update.
