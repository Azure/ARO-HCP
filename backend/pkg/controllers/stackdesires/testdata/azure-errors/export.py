#!/usr/bin/env python3
"""Export only sanitized characterization responses, never setup/identity bodies."""

import argparse
import json
from pathlib import Path
import re


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("evidence", type=Path)
parser.add_argument("--replace", action="store_true", help="replace this probe's generated fixtures")
args = parser.parse_args()
data = json.loads(args.evidence.read_text())
groups = {}
for response in data["responses"]:
    case = response["case"].split("/")[0]
    if re.fullmatch(r"\d\d-[a-z-]+", case):
        groups.setdefault(case, []).append(response)
for case, responses in groups.items():
    payload = json.dumps({"apiVersion": data["apiVersion"], "azureCLI": data["azureCLI"],
                          "responses": responses}, indent=2) + "\n"
    payload = re.sub(r"([?&])([^=\"&]+)=([^\"&]*)",
                     lambda match: match[0] if match[2] == "api-version"
                     else match[1] + match[2] + "=REDACTED", payload)
    payload = re.sub(r"-\d{8}[a-z0-9]{5}", "-GENERATED_SUFFIX", payload)
    if re.search(r"[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}|Bearer |secretText|passwordCredentials", payload):
        raise RuntimeError("Unredacted identifiers or credential fields; refusing export")
    target = Path(__file__).parent / (case + ".json")
    with target.open("w" if args.replace else "x") as output:
        output.write(payload)
    print(target)
target = Path(__file__).parent / "cleanup.json"
with target.open("w" if args.replace else "x") as output:
    json.dump({"cleanup": data["cleanup"], "verification": [
        {"case": response["case"], "method": response["method"],
         "url": response["url"], "status": response["status"]}
        for response in data["responses"] if response["case"] == "cleanup/verify"
    ]}, output, indent=2)
    output.write("\n")
