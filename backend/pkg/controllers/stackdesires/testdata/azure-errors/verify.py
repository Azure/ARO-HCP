#!/usr/bin/env python3
"""Offline checks of captured evidence, with no Azure access or file writes."""

import hashlib
import json
from pathlib import Path
import re


root = Path(__file__).parent
fixtures = {}
for path in root.glob("*.json"):
    text = path.read_text()
    assert not re.search(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}|Bearer |secretText|passwordCredentials", text), path
    for name, value in re.findall(r"[?&]([^=&\"]+)=([^&\"]*)", text):
        assert name == "api-version" or value == "REDACTED", (path, name)
    assert not re.search(r"/home/|/tmp/|[\w.+-]+@[\w.-]+\.[A-Za-z]+|\.onmicrosoft\.com|eyJ[A-Za-z0-9_-]{20}|-----BEGIN", text), path
    if path.name != "cleanup.json":
        fixtures[path.name[:2]] = json.loads(text)["responses"]

expected = {
    "01": ["DeploymentStackDeploymentFailed", "InvalidRequestContent"],
    "02": ["DeploymentStackDeploymentFailed", "InvalidTemplate"],
    "03": ["DeploymentStackDeploymentFailed", "InvalidTemplateDeployment"],
    "05": ["DeploymentStackDeploymentFailed", "InvalidResourceLocation"],
    "06": ["DeploymentStackDeploymentFailed", "DeploymentOutputEvaluationFailed"],
    "07": ["DeploymentStackDeploymentFailed", "InvalidTemplateDeployment", "PreflightValidationFailed",
           "InvalidFederatedIdentityCredentialsAudienceLength"],
    "09": ["DeploymentStackDeploymentFailed", "DeploymentFailed", "DeploymentOutputEvaluationFailed"],
}
for case, codes in expected.items():
    records = fixtures[case]
    assert records[0]["status"] == 201
    assert records[0]["body"]["properties"]["provisioningState"] == "initializing"
    terminal = [record for record in records if record["case"].endswith("/get")][-1]
    assert terminal["status"] == 200
    assert terminal["body"]["properties"]["provisioningState"] == "failed"
    error = terminal["body"]["properties"]["error"]
    for index, code in enumerate(codes):
        assert error["code"] == code, (case, error)
        if index < len(codes) - 1:
            assert len(error["details"]) == 1
            error = error["details"][0]
    assert not error.get("details"), (case, error)

denied = fixtures["03"][0]
retried = fixtures["04"][0]
assert denied["url"] == retried["url"]
assert denied["requestBody"] == retried["requestBody"]
assert denied["requestSHA256"] == retried["requestSHA256"]
assert hashlib.sha256(json.dumps(denied["requestBody"], sort_keys=True).encode()).hexdigest() == denied["requestSHA256"]
assert retried["status"] == 200
assert [r for r in fixtures["04"] if r["case"].endswith("/get")][-1]["body"]["properties"]["provisioningState"] == "succeeded"
for record in fixtures["08"][:2]:
    assert record["status"] == 400
    assert record["body"]["error"]["code"] == "InvalidTemplateDeployment"
    assert not record["body"]["error"].get("details")
    assert "does not have permission" in record["body"]["error"]["message"]
assert fixtures["08"][-1]["status"] == 404
cleanup = json.loads((root / "cleanup.json").read_text())
assert len(cleanup["cleanup"]) == 5
assert set(cleanup["cleanup"].values()) == {"verified absent"}
assert sum(r["status"] == 404 for r in cleanup["verification"]) == 5
print("Nine live-response fixtures, unchanged-intent recovery, sanitization, and cleanup evidence verified")
