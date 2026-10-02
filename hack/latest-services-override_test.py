#!/usr/bin/env python3
import json
import os
from pathlib import Path
import subprocess
import tempfile


root = Path(__file__).resolve().parent.parent
recipe = root.joinpath("Makefile").read_text().split(
    "latest-services-override: $(YQ)\n", 1
)[1].split(".PHONY: latest-services-override", 1)[0]

with tempfile.TemporaryDirectory() as directory:
    scratch = Path(directory)
    # Run the actual recipe with isolated paths and delayed, offline image lookups.
    scratch.joinpath("Makefile").write_text(
        "latest-services-override:\n" + recipe.replace("/tmp/", f"{scratch}/")
    )
    mock = scratch / "submake.py"
    mock.write_text(
        """import json, os, pathlib, sys, time
service = sys.argv[sys.argv.index("-C") + 1]
if service == os.environ.get("FAIL_SERVICE"):
    sys.exit(17)
time.sleep(0.1)
output = next(arg.split("=", 1)[1] for arg in sys.argv if arg.startswith("OVERRIDE_CONFIG_FILE="))
pathlib.Path(output).write_text(json.dumps({"images": {service: "test-digest"}}))
"""
    )
    override = scratch / "leased.yaml"
    override.write_text("infrastructureIdentities:\n  useLeased: true\n")
    output = scratch / "merged.yaml"
    yq = os.environ.get("YQ", "yq")

    def run(extra):
        return subprocess.run(
            [
                "make", "--no-print-directory", "-C", str(scratch),
                "latest-services-override", f"MAKE=python3 {mock}",
                f"YQ={yq}", f"PERS_OVERRIDE_FILE={output}",
                f"PERS_EXTRA_OVERRIDE_FILE={extra}",
            ],
            capture_output=True, text=True,
        )

    for extra in ("", str(override)):
        result = run(extra)
        assert result.returncode == 0, result.stdout + result.stderr
        merged = json.loads(subprocess.check_output([yq, "-o=json", str(output)]))
        assert set(merged["images"]) == {
            "frontend", "backend", "admin", "sessiongate", "mgmt-agent",
            "kube-applier", "fleet", "tooling/aro-hcp-exporter",
        }
        assert ("infrastructureIdentities" in merged) == bool(extra)
        if extra:
            assert merged["infrastructureIdentities"]["useLeased"] is True

    # Stale files from a successful run must not hide a subsequent lookup failure.
    for service in ("frontend", "tooling/aro-hcp-exporter"):
        output.write_text("not-merged\n")
        os.environ["FAIL_SERVICE"] = service
        result = run(str(override))
        assert result.returncode != 0, "image lookup failure was ignored"
        assert output.read_text() == "not-merged\n", "merge ran after lookup failure"
    del os.environ["FAIL_SERVICE"]

print("PASS: delayed lookups finish before merging; leased override preserved; failures stop the merge")
