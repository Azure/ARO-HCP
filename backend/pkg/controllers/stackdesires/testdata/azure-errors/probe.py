#!/usr/bin/env python3
"""Opt-in, operator-run Azure CLI characterization; never part of unit tests."""

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import time
import uuid


ARM = "https://management.azure.com"
# Public Azure built-in role IDs, not environment-specific identifiers.
STACK_ROLE = "bf7f8882-3383-422a-806a-6526c631a88a"
IDENTITY_ROLE = "e40ec5ca-96e0-45a2-b4ff-59039f2c2b59"


def run_cli(arguments, env):
    try:
        return subprocess.run(["az", *arguments, "--output", "json"], env=env,
                              capture_output=True, text=True, timeout=180)
    except Exception:
        # Subprocess/decoding exceptions can contain credential arguments or output.
        raise RuntimeError("Azure CLI invocation failed; command and output withheld") from None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--subscription", required=True, help="expected selected subscription UUID")
    parser.add_argument("--tenant", required=True, help="expected tenant UUID")
    parser.add_argument("--azure-config-dir", required=True, type=Path, help="existing operator Azure CLI profile")
    parser.add_argument("--region", required=True, help="region for the isolated RG and managed identities")
    parser.add_argument("--create-isolated-resources", action="store_true")
    args = parser.parse_args()
    if not args.create_isolated_resources:
        parser.error("explicit --create-isolated-resources opt-in required")
    if not args.azure_config_dir.is_dir():
        parser.error("--azure-config-dir must be an existing directory")
    os.umask(0o077)
    operator = dict(os.environ, AZURE_CONFIG_DIR=str(args.azure_config_dir.resolve()))

    def cli(arguments, env=operator):
        result = run_cli(arguments, env)
        if result.returncode:
            # Do not echo arguments or stderr: credential operations contain secrets.
            raise RuntimeError("Azure CLI returned a failure; command and output withheld")
        try:
            return json.loads(result.stdout) if result.stdout.strip() else None
        except ValueError:
            raise RuntimeError("Azure CLI returned invalid JSON; output withheld") from None

    account = cli(["account", "show"])
    cli_version = cli(["version"])["azure-cli"]
    if (account["id"] != args.subscription or account["tenantId"] != args.tenant
            or account["state"] != "Enabled" or account["environmentName"] != "AzureCloud"):
        raise RuntimeError("Account does not match the authorized subscription/tenant/cloud")
    print("Verified authorized Azure account", flush=True)
    parent = Path(tempfile.gettempdir())
    if not parent.is_dir():
        raise RuntimeError("Temporary parent directory must already exist")
    work = Path(tempfile.mkdtemp(prefix="stack-errors-", dir=parent))
    print(f"Private run directory: {work}", flush=True)
    principal_env = dict(operator, AZURE_CONFIG_DIR=str(work / "principal-config"))
    run_id = uuid.uuid4().hex
    name = "stack-errors-" + run_id
    rg = f"/subscriptions/{args.subscription}/resourceGroups/{name}"
    rg_url = ARM + rg + "?api-version=2024-03-01"
    app = None
    sp = None
    rg_created = False
    assignments = []
    records = []
    replacements = {args.subscription: "SUBSCRIPTION_ID", args.tenant: "TENANT_ID",
                    name: "PROBE_RESOURCE_GROUP", run_id: "RUN_ID"}
    cleanup = {}

    def sanitize(value):
        text = json.dumps(value)
        for actual, placeholder in replacements.items():
            text = text.replace(actual, placeholder)
        text = re.sub(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}",
                      "REDACTED_UUID", text)
        text = re.sub(r"[\w.+-]+@[\w.-]+\.[A-Za-z]+", "REDACTED_EMAIL", text)
        text = re.sub(r"([?&])([^=\"&]+)=([^\"&]*)",
                      lambda match: match[0] if match[2] == "api-version"
                      else match[1] + match[2] + "=REDACTED", text)
        text = re.sub(r"-\d{8}[a-z0-9]{5}", "-GENERATED_SUFFIX", text)
        return json.loads(text)

    def save():
        (work / "responses.sanitized.json").write_text(json.dumps(
            sanitize({"apiVersion": "2024-03-01", "azureCLI": cli_version,
                      "responses": records, "cleanup": cleanup}), indent=2) + "\n")

    def rest(label, method, url, body=None, env=operator):
        command = ["rest", "--method", method, "--url", url, "--verbose"]
        if url.startswith(ARM):
            command += ["--resource", ARM + "/"]
        if body is not None:
            command += ["--body", json.dumps(body)]
        result = run_cli(command, env)
        # --verbose carries the HTTP status and the unflattened error envelope.
        # Never publish request headers (even CLI's truncated bearer token).
        status = re.findall(r"Response status: (\d+)", result.stderr)
        payload = result.stderr.rsplit("Response content:", 1)
        if not status or len(payload) != 2:
            raise RuntimeError(f"{label}: no HTTP response captured (CLI exit {result.returncode})")
        content = re.sub(r"(?m)^INFO: ?", "", payload[-1]).lstrip()
        response = json.JSONDecoder().raw_decode(content)[0] if content.startswith(("{", "[")) else None
        headers = {}
        response_headers = result.stderr.split("Response headers:")[-1].split("Response content:")[0]
        for key, value in re.findall(r"INFO:\s+'([^']+)': '([^']*)'", response_headers):
            if key.lower() in ("azure-asyncoperation", "location", "retry-after", "content-type"):
                headers[key] = value
        record = {"case": label, "method": method.upper(), "url": url,
                  "status": int(status[-1]), "headers": headers, "body": response}
        if body is not None:
            record["requestBody"] = body
            record["requestSHA256"] = hashlib.sha256(json.dumps(body, sort_keys=True).encode()).hexdigest()
        records.append(record)
        save()
        print(f"{label}: {method.upper()} HTTP {record['status']} "
              f"{(response or {}).get('properties', {}).get('provisioningState', '')}", flush=True)
        return record

    def require(record, *statuses):
        if record["status"] not in statuses:
            raise RuntimeError(f"{record['case']}: unexpected HTTP {record['status']}; see sanitized response")
        return record["body"]

    def stack_url(stack):
        return ARM + rg + f"/providers/Microsoft.Resources/deploymentStacks/{stack}?api-version=2024-03-01"

    template = {"$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
                "contentVersion": "1.0.0.0", "resources": []}

    def stack_body(template):
        return {"properties": {
            "template": template, "actionOnUnmanage": {"resources": "delete"},
            "denySettings": {"mode": "none"}}}

    def probe(label, stack, body, env=operator):
        submission = rest(label + "/submit", "put", stack_url(stack), body, env)
        for attempt in range(90):
            observation = rest(label + "/get", "get", stack_url(stack), env=env)
            if submission["status"] >= 400:
                return submission, observation
            require(observation, 200)
            state = observation["body"]["properties"]["provisioningState"].lower()
            if state in ("succeeded", "failed", "canceled"):
                for key, url in submission["headers"].items():
                    if key.lower() == "azure-asyncoperation":
                        rest(label + "/async-operation-get", "get", url, env=env)
                # The deployment has a different error envelope from the stack.
                deployment = observation["body"]["properties"].get("deploymentId")
                if deployment:
                    rest(label + "/deployment-get", "get", ARM + deployment + "?api-version=2024-03-01")
                return submission, observation
            time.sleep(10)
        raise RuntimeError(f"{label}: did not reach terminal state in 15 minutes")

    def assign(role, label):
        assignment = ARM + rg + "/providers/Microsoft.Authorization/roleAssignments/" + str(uuid.uuid4())
        assignments.append(assignment)
        require(rest(label, "put", assignment + "?api-version=2022-04-01", {"properties": {
            "roleDefinitionId": f"/subscriptions/{args.subscription}/providers/Microsoft.Authorization/roleDefinitions/{role}",
            "principalId": sp["id"], "principalType": "ServicePrincipal"}}), 200, 201)

    def interrupted(signum, frame):
        raise RuntimeError(f"Interrupted by signal {signum}; cleaning up")

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    try:
        require(rest("setup/absence", "get", rg_url), 404)
        rg_created = True
        require(rest("setup/create-rg", "put", rg_url,
                     {"location": args.region, "tags": {"stack-errors-owner": run_id}}), 201)
        malformed = copy.deepcopy(template)
        malformed["resources"] = "not-an-array"
        probe("01-malformed-template", "malformed", stack_body(malformed))
        invalid = copy.deepcopy(template)
        invalid["resources"] = [{"type": "Microsoft.ManagedIdentity/userAssignedIdentities",
            "apiVersion": "2023-01-31", "name": "[parameters('notDeclared')]", "location": args.region}]
        probe("02-template-validation", "validation", stack_body(invalid))
        invalid_location = copy.deepcopy(template)
        invalid_location["resources"] = [{"type": "Microsoft.ManagedIdentity/userAssignedIdentities",
            "apiVersion": "2023-01-31", "name": "bad-location", "location": "not-a-real-azure-region"}]
        probe("05-resource-validation", "resource-validation", stack_body(invalid_location))
        bad_output = copy.deepcopy(template)
        bad_output["outputs"] = {"invalid": {"type": "string", "value": "[parameters('notDeclared')]"}}
        probe("06-output-evaluation", "output-evaluation", stack_body(bad_output))
        bad_federation = copy.deepcopy(template)
        bad_federation["resources"] = [
            {"type": "Microsoft.ManagedIdentity/userAssignedIdentities", "apiVersion": "2023-01-31",
              "name": "federation-parent", "location": args.region},
            {"type": "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials",
             "apiVersion": "2023-01-31", "name": "federation-parent/invalid-federation",
             "dependsOn": ["[resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', 'federation-parent')]"],
             "properties": {"issuer": "https://example.invalid", "subject": "probe", "audiences": []}}]
        probe("07-provider-failure", "provider-failure", stack_body(bad_federation))
        nested = copy.deepcopy(template)
        nested["resources"] = [{"type": "Microsoft.Resources/deployments", "apiVersion": "2024-03-01",
            "name": "nested-invalid-output", "properties": {"mode": "Incremental",
            "expressionEvaluationOptions": {"scope": "inner"}, "template": bad_output}}]
        probe("09-nested-deployment-failure", "nested-failure", stack_body(nested))

        # Graph HTTP responses are captured too, but passwords never enter records.
        app = require(rest("setup/create-app", "post", "https://graph.microsoft.com/v1.0/applications",
                           {"displayName": name, "tags": ["stack-errors-owner:" + run_id]}), 201)
        replacements[app["id"]] = "APPLICATION_OBJECT_ID"
        replacements[app["appId"]] = "APPLICATION_CLIENT_ID"
        sp = require(rest("setup/create-sp", "post", "https://graph.microsoft.com/v1.0/servicePrincipals",
                          {"appId": app["appId"]}), 201)
        replacements[sp["id"]] = "SERVICE_PRINCIPAL_ID"
        # Secret is passed only to the child CLI process, never printed or persisted.
        credential = cli(["rest", "--method", "post", "--url",
            f"https://graph.microsoft.com/v1.0/applications/{app['id']}/addPassword", "--body",
            json.dumps({"passwordCredential": {"displayName": name,
                       "endDateTime": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 3600))}})])
        assign(STACK_ROLE, "setup/stack-role")
        for attempt in range(60):
            try:
                accounts = cli(["login", "--service-principal", "--username", app["appId"], "--password",
                     credential["secretText"], "--tenant", args.tenant, "--allow-no-subscriptions"], principal_env)
                if any(account["id"] == args.subscription for account in accounts):
                    break
            except RuntimeError:
                if attempt == 59:
                    raise
            time.sleep(10)
        else:
            raise RuntimeError("Subscription discovery did not observe the new scoped role")
        del credential
        for attempt in range(60):
            ready = rest("setup/wait-stack-role", "get", stack_url("permission"), env=principal_env)
            if ready["status"] == 404:
                break
            require(ready, 403)
            time.sleep(10)
        else:
            raise RuntimeError("Stack role did not propagate")
        contents = copy.deepcopy(template)
        contents["resources"] = [{"type": "Microsoft.ManagedIdentity/userAssignedIdentities",
            "apiVersion": "2023-01-31", "name": "probe-content", "location": args.region}]
        intent = stack_body(contents)
        probe("03-missing-content-permission", "permission", intent, principal_env)
        # Compare the underlying deployment endpoint with the stack's lossy error tree.
        deployment_url = ARM + rg + "/providers/Microsoft.Resources/deployments/direct-permission"
        direct_body = {"properties": {"mode": "Incremental", "template": contents}}
        rest("08-direct-deployment-permission/validate", "post",
             deployment_url + "/validate?api-version=2024-03-01", direct_body, principal_env)
        rest("08-direct-deployment-permission/submit", "put",
             deployment_url + "?api-version=2024-03-01", direct_body, principal_env)
        rest("08-direct-deployment-permission/get", "get",
             deployment_url + "?api-version=2024-03-01", env=principal_env)
        assign(IDENTITY_ROLE, "setup/content-role")
        for attempt in range(30):
            submission, observation = probe("04-same-intent-retry", "permission", intent, principal_env)
            if observation["body"].get("properties", {}).get("provisioningState", "").lower() == "succeeded":
                break
            time.sleep(20)
        else:
            raise RuntimeError("Same intent did not succeed after granting content permission")
    finally:
        # Ignore further interruption while cleaning up; never use an input RG/app ID.
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        failures = []

        def clean(label, operation):
            try:
                operation()
                cleanup[label] = "verified absent"
            except Exception as error:
                cleanup[label] = str(error)
                failures.append(label)
            save()

        def remove(url, expected=None):
            current = rest("cleanup/check", "get", url)
            if current["status"] == 404:
                return
            body = require(current, 200)
            if expected and not expected(body):
                raise RuntimeError("Ownership marker mismatch; refusing deletion")
            require(rest("cleanup/delete", "delete", url), 200, 202, 204)
            for attempt in range(120):
                check = rest("cleanup/verify", "get", url)
                if check["status"] == 404:
                    return
                require(check, 200)
                time.sleep(10)
            raise RuntimeError("Deletion did not complete in 20 minutes")

        for index, assignment in enumerate(assignments):
            clean("role-assignment-" + str(index),
                  lambda assignment=assignment: remove(assignment + "?api-version=2022-04-01",
                      lambda body: body["properties"]["principalId"] == sp["id"]))
        if sp:
            clean("service-principal", lambda: remove("https://graph.microsoft.com/v1.0/servicePrincipals/" + sp["id"],
                  lambda body: body["appId"] == app["appId"]))
        if app:
            clean("application", lambda: remove("https://graph.microsoft.com/v1.0/applications/" + app["id"],
                  lambda body: "stack-errors-owner:" + run_id in body.get("tags", [])))
        if rg_created:
            clean("resource-group", lambda: remove(rg_url,
                  lambda body: body.get("tags", {}).get("stack-errors-owner") == run_id))
        shutil.rmtree(work / "principal-config", ignore_errors=True)
        save()
        print("Cleanup: " + json.dumps(cleanup), flush=True)
        print(f"Sanitized HTTP evidence: {work / 'responses.sanitized.json'}", flush=True)
        if failures:
            raise RuntimeError("Cleanup incomplete: " + ", ".join(failures))


if __name__ == "__main__":
    main()
