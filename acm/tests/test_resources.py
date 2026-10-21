import copy
import json
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import tempfile
import unittest

import yaml


ACM = Path(__file__).resolve().parents[1]
CHARTS = ACM / "deploy/helm"
NONE = {kind: {name: "NONE" for name in ("cpu", "memory")} for kind in ("requests", "limits")}
OVERRIDE = {
    "requests": {"cpu": "111m", "memory": "123Mi"},
    "limits": {"cpu": "222m", "memory": "456Mi"},
}
DIRECT = {
    "backplaneOperator": ("multicluster-engine", "resources.backplaneOperator", "multicluster-engine-operator", "backplane-operator", "100m", "20Mi"),
    "policyAddonController": ("multicluster-engine-config", "policy.grc.resources.policyAddonController", "grc-policy-addon-controller", "manager", "25m", "64Mi"),
    "policyPropagator": ("multicluster-engine-config", "policy.grc.resources.policyPropagator", "grc-policy-propagator", "governance-policy-propagator", "25m", "64Mi"),
    "klusterletAddonController": ("multicluster-engine-config", "policy.cluster-lifecycle.resources.klusterletAddonController", "klusterlet-addon-controller-v2", "klusterlet-addon-controller", "50m", "96Mi"),
}
ADDONS = {
    "configPolicyController": "deployments:config-policy-controller:config-policy-controller",
    "governancePolicyFramework": "deployments:governance-policy-framework:governance-policy-framework-addon",
    "workManager": "deployments:klusterlet-addon-workmgr:acm-agent",
    "hypershiftAddonAgent": "deployments:hypershift-addon-agent:hypershift-addon-agent",
}


def render(chart, settings=None):
    values = {
        "finalizeMceJobImage": "test:latest",
        "localCluster": {"kubeApiUrl": "https://kubernetes.default.svc", "addonConfig": {}},
    }
    for path, value in (settings or {}).items():
        target = values
        parts = path.split(".")
        for part in parts[:-1]:
            target = target.setdefault(part, {})
        target[parts[-1]] = value
    result = subprocess.run(
        ["helm", "template", "resources", str(chart), "--namespace", "multicluster-engine", "-f", "-"],
        input=json.dumps(values), capture_output=True, text=True, check=True,
    )
    return {obj["metadata"]["name"]: obj for obj in yaml.safe_load_all(result.stdout) if obj}


def container(objects, name, container_name):
    return next(c for c in objects[name]["spec"]["template"]["spec"]["containers"] if c["name"] == container_name)


def addon_requirements(objects, hypershift=False):
    if not hypershift:
        return objects["addon-hosted-config"]["spec"].get("resourceRequirements", [])
    return finalize_patch(objects)["spec"].get("resourceRequirements", [])


def finalize_patch(objects):
    script = container(objects, "finalize-mce-config", "finalize")["command"][2]
    subprocess.run(["bash", "-n"], input=script, text=True, check=True)
    line = next(line for line in script.splitlines() if line.startswith("kubectl patch addondeploymentconfig"))
    return json.loads(shlex.split(line)[-1])


class ResourceTests(unittest.TestCase):
    def test_direct_defaults_and_overrides(self):
        for key, (chart, path, deployment, name, cpu, memory) in DIRECT.items():
            with self.subTest(key=key, case="defaults"):
                expected = {"requests": {"cpu": cpu, "memory": memory}}
                if key == "backplaneOperator":
                    expected["limits"] = {"cpu": "100m", "memory": "2Gi"}
                self.assertEqual(container(render(CHARTS / chart), deployment, name)["resources"], expected)
            for kind in ("requests", "limits"):
                for resource in ("cpu", "memory"):
                    with self.subTest(key=key, kind=kind, resource=resource):
                        scalar_expected = copy.deepcopy(expected)
                        scalar_expected.setdefault(kind, {})[resource] = OVERRIDE[kind][resource]
                        objects = render(CHARTS / chart, {f"{path}.{kind}.{resource}": OVERRIDE[kind][resource]})
                        self.assertEqual(container(objects, deployment, name)["resources"], scalar_expected)
            for settings in (OVERRIDE, NONE, {"requests": OVERRIDE["requests"], "limits": NONE["limits"]},
                             {"requests": NONE["requests"], "limits": OVERRIDE["limits"]}):
                with self.subTest(key=key, settings=settings):
                    expected = {kind: quantities for kind, quantities in settings.items() if quantities != NONE[kind]}
                    objects = render(CHARTS / chart, {path: settings})
                    self.assertEqual(container(objects, deployment, name)["resources"], expected)

    def test_finalize_shared_knob(self):
        for chart, job in (("multicluster-engine", "finalize-mce"), ("multicluster-engine-config", "finalize-mce-config")):
            with self.subTest(chart=chart):
                self.assertEqual(container(render(CHARTS / chart), job, "finalize")["resources"], {})
                objects = render(CHARTS / chart, {"resources.finalize": OVERRIDE})
                self.assertEqual(container(objects, job, "finalize")["resources"], OVERRIDE)

    def test_mixed_quantities_and_omission_sentinels(self):
        for settings, expected in (
            ({"requests": {"cpu": 0, "memory": "NONE"},
              "limits": {"cpu": "unlimited", "memory": 1024}},
             {"requests": {"cpu": "0"}, "limits": {"memory": "1024"}}),
            ({"requests": {"cpu": "unlimited", "memory": "1.5Gi"},
              "limits": {"cpu": "2e3", "memory": "NONE"}},
             {"requests": {"memory": "1.5Gi"}, "limits": {"cpu": "2e3"}}),
            ({"requests": {"cpu": "+1", "memory": "NONE"},
              "limits": {"cpu": "unlimited", "memory": "1."}},
             {"requests": {"cpu": "+1"}, "limits": {"memory": "1."}}),
            ({"requests": {"cpu": "+0", "memory": 0},
              "limits": {"cpu": "0", "memory": "+0"}},
             {"requests": {"cpu": "+0", "memory": "0"}, "limits": {"cpu": "0", "memory": "+0"}}),
            ({"requests": {"cpu": "1e-3", "memory": "1E+3"},
              "limits": {"cpu": "+1e+3", "memory": "+1E-3"}},
             {"requests": {"cpu": "1e-3", "memory": "1E+3"}, "limits": {"cpu": "+1e+3", "memory": "+1E-3"}}),
            ({kind: {"cpu": "NONE", "memory": "unlimited"} for kind in NONE}, {}),
        ):
            for key, (chart, path, deployment, name, _, _) in DIRECT.items():
                with self.subTest(key=key, settings=settings):
                    objects = render(CHARTS / chart, {path: settings})
                    self.assertEqual(container(objects, deployment, name)["resources"], expected)
            for chart, job in (("multicluster-engine", "finalize-mce"), ("multicluster-engine-config", "finalize-mce-config")):
                with self.subTest(job=job, settings=settings):
                    objects = render(CHARTS / chart, {"resources.finalize": settings})
                    self.assertEqual(container(objects, job, "finalize")["resources"], expected)
            objects = render(CHARTS / "multicluster-engine-config", {f"resources.{key}": settings for key in ADDONS})
            requirements = addon_requirements(objects) + addon_requirements(objects, True)
            if expected:
                self.assertEqual({item["containerID"] for item in requirements}, set(ADDONS.values()))
                for item in requirements:
                    self.assertEqual(item["resources"], expected)
            else:
                self.assertEqual(requirements, [])
                self.assertNotIn("resourceRequirements", objects["addon-hosted-config"]["spec"])

    def test_finalize_patch_preserves_existing_resources_when_unconfigured(self):
        existing = {
            "apiVersion": "addon.open-cluster-management.io/v1alpha1",
            "kind": "AddOnDeploymentConfig",
            "metadata": {"name": "hypershift-addon-deploy-config", "namespace": "multicluster-engine"},
            "spec": {
                "customizedVariables": [{"name": "disableMetrics", "value": "false"}],
                "resourceRequirements": [{
                    "containerID": ADDONS["hypershiftAddonAgent"],
                    "resources": {"requests": {"cpu": "75m", "memory": "96Mi"}},
                }],
            },
        }
        for settings in (None, NONE, {kind: {"cpu": "NONE", "memory": "unlimited"} for kind in NONE}, OVERRIDE):
            with self.subTest(settings=settings):
                values = {} if settings is None else {"resources.hypershiftAddonAgent": settings}
                patch = finalize_patch(render(CHARTS / "multicluster-engine-config", values))
                self.assertEqual(set(patch), {"spec"})
                self.assertEqual(patch["spec"]["customizedVariables"], [
                    {"name": name, "value": "true"}
                    for name in ("disableMetrics", "disableHOManagement", "autoImportDisabled", "aroHcp")
                ])
                # JSON merge patch replaces supplied lists and preserves absent fields.
                merged = copy.deepcopy(existing)
                merged["spec"].update(patch["spec"])
                if settings == OVERRIDE:
                    self.assertEqual(patch["spec"]["resourceRequirements"], [{
                        "containerID": ADDONS["hypershiftAddonAgent"], "resources": OVERRIDE,
                    }])
                    self.assertEqual(merged["spec"]["resourceRequirements"], patch["spec"]["resourceRequirements"])
                else:
                    self.assertNotIn("resourceRequirements", patch["spec"])
                    self.assertEqual(merged["spec"]["resourceRequirements"], existing["spec"]["resourceRequirements"])

    def test_invalid_quantities_fail_before_hook_json(self):
        targets = [(chart, path) for chart, path, *_ in DIRECT.values()]
        targets += [(chart, "resources.finalize") for chart in ("multicluster-engine", "multicluster-engine-config")]
        targets += [("multicluster-engine-config", f"resources.{key}") for key in ADDONS]
        for chart, path in targets:
            for quantity in ("1';true;#", "$(true)", "1$(true)", "", "1\n", "1NONE", "1unlimited", "1Mi trailing",
                             "-1", "-100m", "-1Mi", "-1e-3", "-0", "-0.0"):
                for resource in ("cpu", "memory"):
                    with self.subTest(path=path, quantity=quantity, resource=resource):
                        with self.assertRaises(subprocess.CalledProcessError) as error:
                            render(CHARTS / chart, {f"{path}.requests.{resource}": quantity})
                        self.assertIn(f"invalid resource quantity for requests.{resource}", error.exception.stderr)

    def test_addon_defaults_and_exact_selectors(self):
        chart = CHARTS / "multicluster-engine-config"
        defaults = render(chart)
        self.assertNotIn("resourceRequirements", defaults["addon-hosted-config"]["spec"])
        self.assertEqual(addon_requirements(defaults, True), [])
        for key, selector in ADDONS.items():
            for kind in ("requests", "limits"):
                for resource in ("cpu", "memory"):
                    with self.subTest(key=key, kind=kind, resource=resource):
                        settings = copy.deepcopy(NONE)
                        settings[kind][resource] = OVERRIDE[kind][resource]
                        objects = render(chart, {f"resources.{key}": settings})
                        expected = [{"containerID": selector, "resources": {kind: {resource: OVERRIDE[kind][resource]}}}]
                        self.assertEqual(addon_requirements(objects, key == "hypershiftAddonAgent"), expected)
                        self.assertEqual(addon_requirements(objects, key != "hypershiftAddonAgent"), [])
        objects = render(chart, {f"resources.{key}": OVERRIDE for key in ADDONS})
        self.assertEqual(len(addon_requirements(objects)), 3)
        self.assertEqual(len(addon_requirements(objects, True)), 1)

    def test_config_path_plumbing(self):
        # Exercise the actual values adapters, including both nested policy charts.
        for chart in ("multicluster-engine", "multicluster-engine-config"):
            adapter = (ACM / f"{chart}.values.yaml").read_text()
            paths = re.findall(r"{{ \.acm\.resources\.([\w.]+) }}", adapter)
            expected_keys = {"backplaneOperator", "finalize"} if chart == "multicluster-engine" else (set(DIRECT) - {"backplaneOperator"}) | set(ADDONS) | {"finalize"}
            self.assertEqual(set(paths), {f"{key}.{kind}.{resource}" for key in expected_keys for kind in NONE for resource in NONE[kind]})
            adapter = re.sub(r"{{ \.acm\.resources\.[\w]+\.(requests|limits)\.(cpu|memory) }}",
                             lambda m: OVERRIDE[m[1]][m[2]], adapter)
            adapter = re.sub(r"{{[^}]+}}", "test", adapter)
            result = subprocess.run(["helm", "template", "resources", str(CHARTS / chart), "-f", "-"],
                                    input=adapter, capture_output=True, text=True, check=True)
            objects = {obj["metadata"]["name"]: obj for obj in yaml.safe_load_all(result.stdout) if obj}
            for key in expected_keys & set(DIRECT):
                _, _, deployment, name, _, _ = DIRECT[key]
                self.assertEqual(container(objects, deployment, name)["resources"], OVERRIDE)
            job = "finalize-mce" if chart == "multicluster-engine" else "finalize-mce-config"
            self.assertEqual(container(objects, job, "finalize")["resources"], OVERRIDE)
            if chart == "multicluster-engine-config":
                for requirement in addon_requirements(objects) + addon_requirements(objects, True):
                    self.assertEqual(requirement["resources"], OVERRIDE)

    def test_generation_is_durable_and_idempotent(self):
        source = (ACM / "resource-templates/_resources.tpl").read_bytes()
        for mode, chart_path in (("mce", "multicluster-engine"), ("policy", "multicluster-engine-config/charts/policy")):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as directory:
                chart = Path(directory) / "chart"
                shutil.copytree(CHARTS / chart_path, chart)
                expected = {p.relative_to(chart): p.read_bytes() for p in chart.rglob("*") if p.is_file()}
                helper = chart / "templates/_resources.tpl"
                self.assertEqual(helper.read_bytes(), source)
                helper.unlink()
                changed = []
                for path in chart.rglob("*.yaml"):
                    original = path.read_text()
                    upstream = re.sub(r'          {{- include "acm.resources" .Values.resources.\w+ \| nindent 10 }}',
                                      "          requests:\n            cpu: 999m\n            memory: 999Mi", original)
                    if upstream != original:
                        if "finalize-mce.job" in path.name:
                            upstream = re.sub(r"        resources:\n          requests:\n            cpu: 999m\n            memory: 999Mi\n", "", upstream)
                        path.write_text(upstream)
                        changed.append(path)
                if mode == "mce":
                    values = chart / "values.yaml"
                    defaults = yaml.safe_load(values.read_text())
                    defaults.pop("resources")
                    values.write_text(yaml.safe_dump(defaults))
                command = ["bash", str(ACM / "update-resource-templates.sh"), mode, str(chart)]
                subprocess.run(command, check=True)
                for path, content in expected.items():
                    self.assertEqual((chart / path).read_bytes(), content, str(path))
                subprocess.run(command, check=True)
                for path, content in expected.items():
                    self.assertEqual((chart / path).read_bytes(), content, str(path))
                # An upstream layout change must not silently remove the knob.
                changed[0].write_text("kind: Deployment\n")
                self.assertNotEqual(subprocess.run(command, capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
