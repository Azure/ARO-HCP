#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
from pathlib import Path


def load_json(path: Path) -> object:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"{path} is not valid JSON: {exc}") from exc


def main() -> int:
    sre_root = Path(__file__).resolve().parents[2]
    errors: list[str] = []

    def require(condition: bool, message: str) -> None:
        if not condition:
            errors.append(message)

    def require_file(rel: str) -> None:
        require((sre_root / rel).exists(), f"missing required file: {rel}")

    required_files = [
        "SKILL.md",
        "MANIFEST.md",
        "AGENTS.md",
        "Makefile",
        "agents/arohcp-sre-agent.md",
        "agents/arohcp-sre-kube-apiserver.md",
        "sub-investigators/cross-cutting.md",
        "sub-investigators/kube-apiserver.md",
        "common/symptom-routing/routing.json",
        "common/output-contract/tsg-format.md",
        "common/output-contract/domain-memo-format.md",
        "common/investigation/incident-envelope.md",
        "common/investigation/evidence-ladder.md",
        "common/investigation/observability-gap-branch.md",
        "common/investigation/fresh-session-domain-flow.md",
        "common/security-privacy/redaction-rules.md",
        "common/self-check/final-pass.md",
        "common/scope-boundaries/non-goals.md",
        "common/tools/validate_sre_assets.py",
        "common/tools/smoke_sre_agent.py",
        "fixtures/historical-incidents/incident-002-kas-api-availability-burn.md",
        "tests/README.md",
    ]
    for rel in required_files:
        require_file(rel)

    absent_paths = [
        "agents/arohcp-sre-resource-provider-api.md",
        "sub-investigators/resource-provider-api.md",
        "common/query-playbooks",
        "common/tools/build_incident_bundle.py",
        "common/tools/prepare_mirror_snapshot.py",
        "common/tools/resolve_openshift_ref.py",
        "common/tools/repo_snapshot.py",
        "common/tools/validate_incident_bundle.py",
        "common/tools/validate_payload_version_mapping.py",
        "fixtures/evidence-ingestion",
        "fixtures/historical-incidents/incident-001-frontend-arm-request-failure.md",
    ]
    for rel in absent_paths:
        require(not (sre_root / rel).exists(), f"kernel PR should not include: {rel}")

    skill_text = (sre_root / "SKILL.md").read_text(encoding="utf-8")
    runtime_text = (sre_root / "agents/arohcp-sre-agent.md").read_text(encoding="utf-8")
    domain_text = (sre_root / "agents/arohcp-sre-kube-apiserver.md").read_text(encoding="utf-8")
    agents_text = (sre_root / "AGENTS.md").read_text(encoding="utf-8")
    output_contract_text = (sre_root / "common/output-contract/tsg-format.md").read_text(encoding="utf-8")
    final_pass_text = (sre_root / "common/self-check/final-pass.md").read_text(encoding="utf-8")
    makefile_text = (sre_root / "Makefile").read_text(encoding="utf-8")

    require("context: fork" in skill_text, "SKILL.md must declare context: fork")
    require(
        "disable-model-invocation: true" in skill_text,
        "SKILL.md must disable direct model invocation",
    )
    require(
        "agent: arohcp-sre-agent" in skill_text,
        "SKILL.md must dispatch to arohcp-sre-agent",
    )
    require(
        "Return only the forked runtime agent's TSG draft." in skill_text,
        "SKILL.md must require returning the runtime TSG without wrapper text",
    )
    require("MANIFEST.md" in agents_text, "AGENTS.md must point readers to MANIFEST.md")
    require("name: arohcp-sre-agent" in runtime_text, "runtime agent name must match SKILL.md agent")
    require(
        "domain-memo-format.md" in runtime_text,
        "runtime agent must load the domain memo contract",
    )
    require(
        "fresh-session-domain-flow.md" in runtime_text,
        "runtime agent must load fresh-session flow guidance",
    )
    require(
        "observability-gap-branch.md" in runtime_text,
        "runtime agent must load the observability-gap guidance",
    )
    require(
        "launch the router-listed `domain_agent_name` in a fresh session" in runtime_text,
        "runtime agent must require fresh-session domain fanout",
    )
    require(
        "The first non-whitespace line MUST be `# TSG: <short incident title>`." in runtime_text,
        "runtime agent must require TSG output to start with the title line",
    )
    require(
        "Use the headings from `common/output-contract/tsg-format.md` in the same order." in runtime_text,
        "runtime agent must require the canonical TSG heading order",
    )
    require(
        "name: arohcp-sre-kube-apiserver" in domain_text,
        "domain agent name must match router metadata",
    )
    require("## Template" in output_contract_text, "output contract must embed the TSG Template")
    require(
        "Incident envelope" in output_contract_text,
        "output contract must include Incident envelope metadata",
    )
    require(
        "Output begins with `# TSG:` and contains no text before the title." in final_pass_text,
        "final self-check must verify the TSG title is first",
    )
    require(
        "Metadata includes `Incident envelope`." in final_pass_text,
        "final self-check must verify incident envelope metadata",
    )
    require("smoke:" in makefile_text, "Makefile must include a smoke target")

    routing = load_json(sre_root / "common/symptom-routing/routing.json")
    domains: list[object] = []
    always_load: list[object] = []
    if isinstance(routing, dict):
        raw_domains = routing.get("domains", [])
        if isinstance(raw_domains, list):
            domains = raw_domains
        else:
            require(False, "routing domains must be a JSON array")

        raw_always_load = routing.get("always_load", [])
        if isinstance(raw_always_load, list):
            always_load = raw_always_load
        else:
            require(False, "routing always_load must be a JSON array")

        require(bool(domains), "routing must define at least one domain")
        require(len(domains) == 1, "kernel PR routing must define exactly one domain")
        if domains:
            domain = domains[0]
            require(isinstance(domain, dict), "routing domain entries must be JSON objects")
            if isinstance(domain, dict):
                require(
                    domain.get("id") == "kube-apiserver",
                    "kernel PR domain must be kube-apiserver",
                )
                require(
                    domain.get("domain_agent_name") == "arohcp-sre-kube-apiserver",
                    "routing must point to the kube-apiserver child agent",
                )
                require(
                    domain.get("sub_investigator") == "sub-investigators/kube-apiserver.md",
                    "routing must point to the kube-apiserver investigator",
                )
                history_fixtures = domain.get("history_fixtures", [])
                require(
                    isinstance(history_fixtures, list),
                    "routing history_fixtures must be a JSON array",
                )
                if isinstance(history_fixtures, list):
                    for rel in history_fixtures:
                        require(isinstance(rel, str), "routing fixture entries must be strings")
                        if isinstance(rel, str):
                            require((sre_root / rel).exists(), f"missing routing fixture: {rel}")

        for rel in always_load:
            require(isinstance(rel, str), "always_load entries must be strings")
            if isinstance(rel, str):
                require((sre_root / rel).exists(), f"missing always_load asset: {rel}")
    else:
        require(False, "routing.json must contain a JSON object at the top level")

    if errors:
        for error in errors:
            print(error, file=sys.stderr)
        return 1

    print(f"Validated kernel SRE agent assets for {len(domains)} routed domain.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
