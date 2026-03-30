# SRE Agent Tests

The kernel PR keeps testing intentionally small.

## Automated validation

Run:

```bash
make -C tooling/sre-agent validate
```

This checks:

- `SKILL.md` dispatch to the runtime agent
- required kernel asset files exist
- routing resolves to exactly one child domain
- the proof fixture exists and is wired into routing
- the runtime loads the fresh-session domain flow and domain memo contract

## Manual smoke

Run:

```bash
make -C tooling/sre-agent smoke
```

Confirm that the printed prompt produces a draft that:

- starts with `# TSG:` and has no prose before the title
- includes `Incident envelope` in metadata
- uses the fresh-session kube-apiserver child-agent flow
- distinguishes probe-path degradation from confirmed kube-apiserver failure
- keeps mitigation advisory-only
