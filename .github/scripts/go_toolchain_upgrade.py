#!/usr/bin/env python3
"""Plan, apply and guard same-minor Go patch upgrades without editing dependencies.

Requires Python 3.10+, PyYAML, gh, git, Docker and Node 20+ (npx); apply/check
additionally require the target Go binary on PATH. Discovery reads external
services and builds a disposable proposed devcontainer. Compiler probes have
no network, mounts, credentials or capabilities. Plan waits exit 0; a blocked
publication recheck exits 3, while configuration/authentication errors exit 1.
"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess
import sys
import tempfile
import uuid
import urllib.request

import yaml


VERSION = re.compile(r"1\.(\d+)\.(\d+)")
DIRECTIVE = re.compile(r"^(go|toolchain)[ \t]+(go)?(1\.\d+(?:\.\d+)?)(?:[ \t]*(?://[^\n]*)?)$", re.MULTILINE)
RECIPE = re.compile(r"(printf 'module [^'\n]+\\n\\ngo )(1\.\d+\.\d+)(\\n' > \$\(TYPESPEC_GO_OUTPUT_DIR\)/go\.mod)")
RELEASE_PATH = "ci-operator/config/Azure/ARO-HCP"
DEVCONFIG = ".devcontainer/devcontainer.json"
DEVCONTAINERS_CLI = "@devcontainers/cli@0.89.0"


class Blocked(Exception):
    """Known unavailable prerequisite; wait rather than publish."""


def run(args, *, cwd=None, env=None, timeout=120):
    return subprocess.run(args, cwd=cwd, env=env, check=True, text=True, timeout=timeout,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout.strip()


def go_env():
    return dict(os.environ, GOTOOLCHAIN="local", GOWORK="off", GOFLAGS="")


def version(value):
    match = VERSION.fullmatch(value)
    if not match:
        raise ValueError(f"expected stable Go patch version, got {value!r}")
    return (1, int(match[1]), int(match[2]))


def directives(text):
    found = {}
    for match in DIRECTIVE.finditer(text):
        key, prefix, value = match.groups()
        if key in found or (key == "toolchain" and prefix != "go") or (key == "go" and prefix):
            raise ValueError("duplicate or invalid Go directive")
        # Go permits a language-only minimum; retain its spelling in snapshots.
        version(value if value.count(".") == 2 else value + ".0")
        found[key] = value
    if "go" not in found:
        raise ValueError("missing or unsupported Go directive")
    # Do not overlook future or unsupported toolchain forms such as default.
    declared = re.findall(r"^(go|toolchain)\s", text, re.MULTILINE)
    if sorted(declared) != sorted(found):
        raise ValueError("unsupported Go/toolchain directive")
    return found


def stable(value):
    return value if value.count(".") == 2 else value + ".0"


def latest_patch(current, releases):
    current_version = version(current)
    candidates = [item["version"][2:] for item in releases
                  if item.get("stable") is True and isinstance(item.get("version"), str)
                  and VERSION.fullmatch(item["version"][2:])
                  and version(item["version"][2:])[:2] == current_version[:2]]
    if not candidates:
        raise Blocked(f"official Go download feed has no stable release for {current_version[0]}.{current_version[1]}")
    target = max(candidates, key=version)
    if version(target) < current_version:
        raise Blocked(f"checkout Go {current} is newer than official same-minor release {target}; refusing downgrade")
    return target


def gh_json(endpoint):
    return json.loads(run(["gh", "api", endpoint]))


def gh_pages(endpoint):
    pages = json.loads(run(["gh", "api", "--paginate", "--slurp", endpoint]))
    if not isinstance(pages, list) or any(not isinstance(page, list) for page in pages):
        raise ValueError(f"incomplete paginated GitHub response: {endpoint}")
    return [item for page in pages for item in page]


def tracked(root):
    return sorted(run(["git", "ls-files", "-z"], cwd=root).split("\0"))


def safe_file(root, name):
    path = PurePosixPath(name)
    if path.is_absolute() or ".." in path.parts or not path.parts:
        raise ValueError(f"unsafe tracked path: {name}")
    local = root / name
    if local.is_symlink() or not local.is_file() or not local.resolve().is_relative_to(root):
        raise ValueError(f"not a regular checkout file: {name}")
    return local


def digest_text(text):
    return hashlib.sha256(text.encode()).hexdigest()


def inventory(root):
    files = [name for name in tracked(root) if name]
    recipe = safe_file(root, "api/Makefile").read_text()
    sdk_outputs = re.findall(r"^TESTSDK_OUTPUT_DIR\s*[:?]?=\s*(\.\./[^$\s]+)/\$(?:\{VERSION\}|\(VERSION\))/", recipe, re.MULTILINE)
    if len(sdk_outputs) != 1:
        raise ValueError("api/Makefile: cannot discover exactly one generated TESTSDK_OUTPUT_DIR root")
    sdk_root = (root / "api" / sdk_outputs[0]).resolve()
    if not sdk_root.is_relative_to(root) or sdk_root == root:
        raise ValueError("api/Makefile: generated SDK output escapes checkout")
    sdk_prefix = sdk_root.relative_to(root).as_posix() + "/"
    all_manifests = [name for name in files if PurePosixPath(name).name in ("go.mod", "go.work")]
    excluded = [{"path": name, "reason": "Bingo tool metadata" if name.startswith(".bingo/") else "generated API SDK"}
                for name in all_manifests if name.startswith(".bingo/") or name.startswith(sdk_prefix)]
    excluded_paths = {item["path"] for item in excluded}
    manifests = [name for name in all_manifests if name not in excluded_paths]
    if "go.work" not in manifests or not any(name.endswith("go.mod") for name in manifests):
        raise ValueError("incomplete tracked Go workspace inventory")
    all_texts = {name: safe_file(root, name).read_text() for name in all_manifests}
    texts = {name: all_texts[name] for name in manifests}
    module_data = []
    for name, text in texts.items():
        try:
            parsed = directives(text)
        except ValueError as exc:
            raise ValueError(f"{name}: {exc}") from exc
        module_data.append({"path": name, "directives": parsed, "sha256": digest_text(text)})
    # Validate workspace membership, including explicit references not found by ls-files.
    workspace = re.sub(r"//[^\n]*", "", texts["go.work"])
    uses = re.findall(r"^\s*(?:use\s+)?(\./[^\s()]+)\s*$", workspace, re.MULTILINE)
    if not uses:
        raise ValueError("cannot discover workspace use directives")
    for use in uses:
        if str(PurePosixPath(use) / "go.mod") not in texts:
            raise ValueError(f"workspace module missing from tracked inventory: {use}")
    matches = list(RECIPE.finditer(recipe))
    if len(matches) != 1:
        raise ValueError("expected exactly one API module Go generator recipe")
    snapshots = dict(all_texts, **{"api/Makefile": recipe})
    snapshots[DEVCONFIG] = safe_file(root, DEVCONFIG).read_text()
    for name in files:
        if PurePosixPath(name).name in ("go.sum", "go.work.sum"):
            snapshots[name] = safe_file(root, name).read_text()
    return {"modules": module_data, "excluded_modules": excluded, "files": files, "snapshots": snapshots,
            "recipe_version": matches[0][2]}


def upgrade_patch(files, devconfigs=None):
    """Identify actual compiler changes, never PR author, title or checksum text."""
    for item in files:
        name = item["filename"]
        if PurePosixPath(name).name not in ("go.mod", "go.work") and name not in ("api/Makefile", DEVCONFIG):
            continue
        if name == DEVCONFIG:
            if devconfigs is None:
                raise Blocked("cannot inspect complete base/head devcontainer compiler configuration")
            def compiler_config(text):
                config = jsonc(text)
                feature = go_feature(config)
                return (config.get("image"), config.get("build"),
                        (feature[0], config["features"][feature[0]]) if feature else None)
            if compiler_config(devconfigs[0]) != compiler_config(devconfigs[1]):
                return True
            continue
        patch = item.get("patch")
        if not isinstance(patch, str):
            raise Blocked(f"cannot inspect full compiler-related diff for {name}")
        removed, added = [], []
        for line in patch.splitlines():
            if not line or line[:1] not in ("+", "-") or line.startswith(("+++", "---")):
                continue
            body = line[1:]
            is_directive = name != "api/Makefile" and re.match(r"^(?:go|toolchain)\s", body)
            is_recipe = name == "api/Makefile" and RECIPE.search(body)
            if is_directive or is_recipe:
                (added if line[0] == "+" else removed).append(body)
        if removed != added:
            return True
    return False


def open_upgrades(repo):
    """Return the first proven blocker; [] requires inspecting every open PR."""
    for pr in gh_pages(f"repos/{repo}/pulls?state=open&per_page=100"):
        files = gh_pages(f"repos/{repo}/pulls/{pr['number']}/files?per_page=100")
        # GitHub's PR-files endpoint truncates at 3,000: never silently accept it.
        detail = gh_json(f"repos/{repo}/pulls/{pr['number']}")
        if len(files) != detail["changed_files"]:
            raise Blocked(f"cannot inspect all files of open PR #{pr['number']}")
        devconfigs = None
        if any(item["filename"] == DEVCONFIG for item in files):
            configs = []
            for side in ("base", "head"):
                endpoint = detail[side]
                repository = endpoint.get("repo")
                if not repository:
                    raise Blocked(f"cannot inspect {side} devcontainer config of PR #{pr['number']}")
                content = gh_json(f"repos/{repository['full_name']}/contents/{DEVCONFIG}?ref={endpoint['sha']}")
                if content.get("encoding") != "base64":
                    raise Blocked(f"incomplete {side} devcontainer config of PR #{pr['number']}")
                configs.append(base64.b64decode(content["content"]).decode())
            devconfigs = tuple(configs)
        if upgrade_patch(files, devconfigs):
            return [{"number": pr["number"], "url": pr["html_url"]}]
    return []


def image_stream(ref):
    if not isinstance(ref, dict) or set(("namespace", "name", "tag")) - ref.keys():
        raise Blocked(f"unsupported CI image stream reference: {ref!r}")
    values = [ref[key] for key in ("namespace", "name", "tag")]
    if any(not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9_.-]+", value) for value in values):
        raise Blocked("invalid CI image stream reference")
    return f"registry.ci.openshift.org/{values[0]}/{values[1]}:{values[2]}"


def docker_stages(text):
    """Resolve literal FROMs and ARG defaults without executing Dockerfiles."""
    defaults, stages = {}, []
    for line in text.splitlines():
        arg = re.match(r"^ARG\s+(\w+)=(\S+)\s*$", line, re.IGNORECASE)
        if arg:
            defaults[arg[1]] = arg[2]
        match = re.match(r"^FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?\s*$", line, re.IGNORECASE)
        if not match:
            if stages:
                stages[-1]["body"] += line + "\n"
            continue
        image = re.sub(r"\$\{(\w+)\}|\$(\w+)", lambda m: defaults.get(m[1] or m[2], m[0]), match[1])
        if "$" in image:
            raise Blocked(f"unresolved Dockerfile base image: {image}")
        stages.append({"image": image, "alias": match[2], "body": ""})
    if not stages:
        raise Blocked("Dockerfile has no discoverable FROM instruction")
    return stages


def jsonc(text):
    # Preserve URLs/comment characters in quoted strings.
    return json.loads(re.sub(r'("(?:\\.|[^"\\])*"|//[^\n]*|/\*[\s\S]*?\*/)',
                             lambda m: m[0] if m[0].startswith('"') else "", text))


def discover_builders(root, repo):
    """Follow current release config roots; never guess an OpenShift tag."""
    if repo != "Azure/ARO-HCP":
        raise ValueError("builder provenance currently supports Azure/ARO-HCP only")
    images = {}
    sources = []
    projects = []

    def add(image, source):
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._/:@-]+", image) or image.startswith("-"):
            raise Blocked(f"unsupported image reference in {source}: {image}")
        images.setdefault(image, []).append(source)

    local = yaml.safe_load(safe_file(root, ".ci-operator.yaml").read_text())
    local_root = image_stream(local["build_root_image"])
    add(local_root, ".ci-operator.yaml")
    release_sha = default_head("openshift/release")
    listing = gh_json(f"repos/openshift/release/contents/{RELEASE_PATH}?ref={release_sha}")
    configs = [item for item in listing if item["name"].startswith("Azure-ARO-HCP-main") and item["name"].endswith(".yaml")]
    if not configs or not any(item["name"] == "Azure-ARO-HCP-main.yaml" for item in configs):
        raise Blocked("live openshift/release main config inventory incomplete")
    for item in configs:
        content = gh_json(f"repos/openshift/release/contents/{item['path']}?ref={release_sha}")
        if content.get("encoding") != "base64":
            raise ValueError("unsupported GitHub config content encoding")
        config = yaml.safe_load(base64.b64decode(content["content"]).decode())
        sources.append({"path": item["path"], "sha": content["sha"], "revision": release_sha})
        build_root = config.get("build_root", {})
        if not build_root and not any(config.get(key) for key in ("binary_build_commands", "test_binary_build_commands", "images")):
            # Periodic jobs consume already-built executables, not a Go root.
            continue
        if set(build_root) == {"from_repository"} and build_root["from_repository"] is True:
            add(local_root, item["path"])
        elif "image_stream_tag" in build_root and set(build_root) <= {"image_stream_tag", "use_build_cache"}:
            add(image_stream(build_root["image_stream_tag"]), item["path"])
        elif set(build_root) == {"project_image"}:
            project = build_root["project_image"]
            if not isinstance(project, dict) or set(project) != {"dockerfile_literal"}:
                raise Blocked(f"unsupported project build root in {item['path']}")
            literal = project["dockerfile_literal"]
            for stage in docker_stages(literal):
                if "/" in stage["image"]:
                    add(stage["image"], item["path"] + ":project_image")
            projects.append({"source": item["path"], "dockerfile": literal})
        else:
            raise Blocked(f"unsupported build_root in {item['path']}")
        # External source aliases actually used as image parents or test runners.
        # Base-image-generator ingredients are not runners: published outputs are
        # inspected through the consuming main configs instead.
        aliases = config.get("base_images", {})
        if "__baseimage-generator" not in item["name"]:
            def walk(value):
                if isinstance(value, dict):
                    for key, child in value.items():
                        if key == "from" and isinstance(child, str) and child in aliases:
                            add(image_stream(aliases[child]), f"{item['path']}:{child}")
                        walk(child)
                elif isinstance(value, list):
                    for child in value:
                        walk(child)
            walk(config)
    files = [name for name in tracked(root) if name]
    dockerfiles = [name for name in files if PurePosixPath(name).name.startswith(("Dockerfile", "Containerfile"))
                   and not name.endswith((".dockerignore", ".md"))]
    if not dockerfiles:
        raise Blocked("no tracked Dockerfile inventory")
    for name in dockerfiles:
        text = safe_file(root, name).read_text()
        stages = docker_stages(text)
        aliases = {stage["alias"] for stage in stages if stage["alias"]}
        for stage in stages:
            image = stage["image"]
            compiler = bool(re.search(r"(?:golang|/builder:)", image))
            invokes_go = bool(re.search(r"\bgo\s+(?:build|test|install|run|version|env|clean)\b", stage["body"]))
            if image not in aliases and (compiler or invokes_go):
                add(image, name)
    dev = jsonc(safe_file(root, DEVCONFIG).read_text())
    if not isinstance(dev.get("image"), str) or "build" in dev:
        raise Blocked("unsupported devcontainer compiler provenance")
    feature = go_feature(dev)
    if feature is None:
        add(dev["image"], DEVCONFIG + ":image")
    return images, sources, projects


def go_feature(dev):
    features = [(key, value) for key, value in dev.get("features", {}).items()
                if re.search(r"/go(?::|@)", key)]
    if len(features) > 1:
        raise Blocked("multiple devcontainer Go features are unsupported")
    if not features:
        return None
    key, feature = features[0]
    configured = feature.get("version") if isinstance(feature, dict) else None
    if not isinstance(configured, str) or not VERSION.fullmatch(configured):
        raise Blocked("devcontainer Go feature is not an exact stable version")
    return key, configured


def edit_devcontainer(text, target):
    dev = jsonc(text)
    feature = go_feature(dev)
    if feature is None:
        return text
    key, current = feature
    if version(current) > version(target):
        raise Blocked(f"devcontainer Go feature {current} is newer than target; refusing downgrade")
    pattern = re.compile(r'("' + re.escape(key) + r'"\s*:\s*\{[^{}]*"version"\s*:\s*")'
                         + re.escape(current) + r'(")')
    updated, count = pattern.subn(lambda m: m[1] + target + m[2], text)
    if count != 1:
        raise Blocked("cannot safely target exactly one devcontainer Go feature version")
    dev["features"][key]["version"] = target
    if jsonc(updated) != dev:
        raise ValueError("devcontainer feature edit changed unrelated configuration")
    return updated


def execute_compiler(immutable):
    output = run(["docker", "run", "--rm", "--pull=never", "--platform", "linux/amd64",
                  "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
                  "--pids-limit=32", "--memory=256m", "--cpus=1", "--user=65534:65534",
                  "--env", "GOTOOLCHAIN=local", "--env", "GOENV=off", "--entrypoint", "go", immutable, "version"])
    match = re.fullmatch(r"go version go(1\.\d+\.\d+) linux/amd64", output)
    if not match:
        raise Blocked(f"builder {immutable} returned unsupported compiler: {output!r}")
    return match[1]


def probe_failure(context, exc):
    """Only known missing artifacts/transient availability become normal waits."""
    if isinstance(exc, subprocess.TimeoutExpired):
        raise Blocked(f"{context}: probe exceeded its execution deadline") from exc
    if not isinstance(exc, subprocess.CalledProcessError):
        raise exc
    detail = "\n".join(filter(None, (exc.stdout, exc.stderr)))
    if re.search(r"unauthorized|forbidden|denied|insufficient[_ ]scope|authentication required|\b40[13]\b", detail, re.IGNORECASE):
        raise ValueError(f"{context}: authentication/authorization failure: {detail.strip()}") from exc
    unavailable = (r"manifest unknown|manifest[^\n]*not found|registry unavailable|"
                   r"connection refused|connection reset|timed out|i/o timeout|temporary failure|"
                   r"no such host|could not resolve|cannot connect to the docker daemon|"
                   r"executable file not found|go: not found")
    if re.search(unavailable, detail, re.IGNORECASE):
        raise Blocked(f"{context}: {detail.strip()}") from exc
    raise exc


def probe_devcontainer(root, target):
    """Build the proposed effective compiler, not the overridden base Go image.

    `build` does not run lifecycle hooks or start/mount the workspace. The
    temporary context contains only the proposed config, never repository data.
    Locally built composites have an immutable Docker content ID, not a registry
    RepoDigest; execute that ID directly and record the distinction.
    """
    proposed = edit_devcontainer(safe_file(root, DEVCONFIG).read_text(), target)
    feature = go_feature(jsonc(proposed))
    if feature is None:
        return None
    name = "aro-hcp-go-toolchain-probe:" + uuid.uuid4().hex
    try:
        with tempfile.TemporaryDirectory(prefix="aro-hcp-go-devcontainer-") as temp:
            workspace = Path(temp)
            config = workspace / ".devcontainer" / "devcontainer.json"
            config.parent.mkdir()
            config.write_text(proposed)
            result = json.loads(run(["npx", "--yes", DEVCONTAINERS_CLI, "build",
                                     "--workspace-folder", str(workspace), "--config", str(config),
                                     "--image-name", name, "--platform", "linux/amd64"], timeout=1800))
            if result.get("outcome") != "success":
                raise Blocked("devcontainer CLI did not report a successful image build")
            immutable = run(["docker", "image", "inspect", "--format", "{{.Id}}", name])
            if not re.fullmatch(r"sha256:[0-9a-f]{64}", immutable):
                raise Blocked("devcontainer build has no immutable Docker image ID")
            actual = execute_compiler(immutable)
            if actual != target:
                raise Blocked(f"proposed devcontainer has Go {actual}, expected exactly {target}")
            return {"image": "devcontainer:" + jsonc(proposed)["image"], "digest": immutable,
                    "digest_kind": "docker-image-id", "version": actual, "platform": "linux/amd64",
                    "sources": [DEVCONFIG], "feature": feature[0], "cli": DEVCONTAINERS_CLI,
                    "config_sha256": digest_text(proposed)}
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError) as exc:
        probe_failure("cannot prove proposed devcontainer compiler", exc)
    finally:
        # Unique temporary tag only; never delete a caller's/shared image.
        try:
            run(["docker", "image", "rm", name])
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError):
            pass


def probe_builder(image):
    """Resolve the tag once, then execute only its immutable linux/amd64 digest."""
    try:
        run(["docker", "pull", "--platform", "linux/amd64", image], timeout=300)
        digests = json.loads(run(["docker", "image", "inspect", "--format", "{{json .RepoDigests}}", image]))
        if not digests:
            raise Blocked(f"builder {image} has no immutable repository digest")
        repository = image.split("@")[0].rsplit("/", 1)
        repository[-1] = repository[-1].split(":")[0]
        repository = "/".join(repository)
        matches = [value for value in digests if value.startswith(repository + "@sha256:")]
        if len(matches) != 1 or not re.fullmatch(r"[^\s]+@sha256:[0-9a-f]{64}", matches[0]):
            raise Blocked(f"builder {image} has ambiguous immutable digest")
        immutable = matches[0]
        actual = execute_compiler(immutable)
        return {"image": image, "digest": immutable, "version": actual, "platform": "linux/amd64"}
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError) as exc:
        probe_failure(f"cannot prove compiler in builder {image}", exc)


def probe_project(project):
    """Build the actual live inline CI root, which has no published image tag."""
    name = "aro-hcp-go-project-probe:" + uuid.uuid4().hex
    source = project["source"]
    dockerfile = re.sub(r"\\\n", " ", project["dockerfile"])
    if re.search(r"--(?:network\s*=\s*['\"]?host|security\s*=\s*['\"]?insecure|privileged)\b|"
                 r"--mount\s*=[^\n]*\btype\s*=\s*['\"]?(?:secret|ssh)\b|"
                 r"^\s*#\s*syntax\s*=", dockerfile, re.IGNORECASE | re.MULTILINE):
        raise ValueError(f"unsafe privileged/host-network/secret build directive in {source}")
    builder_name = "aro-hcp-go-project-" + uuid.uuid4().hex
    try:
        with tempfile.TemporaryDirectory(prefix="aro-hcp-go-project-") as temp:
            Path(temp, "Dockerfile").write_text(project["dockerfile"])
            run(["docker", "buildx", "create", "--name", builder_name, "--driver", "docker-container",
                 "--driver-opt", "memory=2g", "--driver-opt", "memory-swap=2g",
                 "--driver-opt", "cpu-period=100000", "--driver-opt", "cpu-quota=200000"])
            run(["docker", "buildx", "build", "--builder", builder_name, "--load", "--pull",
                 "--platform", "linux/amd64", "--tag", name, temp], timeout=1800)
            immutable = run(["docker", "image", "inspect", "--format", "{{.Id}}", name])
            if not re.fullmatch(r"sha256:[0-9a-f]{64}", immutable):
                raise Blocked(f"project root {source} has no immutable Docker image ID")
            return {"image": "project-image:" + source, "digest": immutable, "digest_kind": "docker-image-id",
                    "version": execute_compiler(immutable), "platform": "linux/amd64", "sources": [source],
                    "dockerfile_sha256": digest_text(project["dockerfile"])}
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError) as exc:
        probe_failure(f"cannot prove inline CI build root {source}", exc)
    finally:
        try:
            run(["docker", "buildx", "rm", "--force", builder_name])
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError):
            pass
        try:
            run(["docker", "image", "rm", name])
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, FileNotFoundError):
            pass


def builder_readiness(root, repo, target):
    images, sources, projects = discover_builders(root, repo)
    builders, reasons = [], []
    for image, provenance in sorted(images.items()):
        try:
            builder = probe_builder(image)
            builder["sources"] = provenance
            builders.append(builder)
            if version(builder["version"]) < version(target):
                reasons.append(f"builder {image} has Go {builder['version']}, needs at least {target}")
        except Blocked as exc:
            reasons.append(str(exc))
            builders.append({"image": image, "digest": None, "version": None, "sources": provenance})
    if reasons:
        # No need to build expensive composites while a mandatory base is old.
        return builders, sources, reasons
    for project in projects:
        try:
            builder = probe_project(project)
            builders.append(builder)
            if version(builder["version"]) < version(target):
                reasons.append(f"CI project root {project['source']} has Go {builder['version']}, needs at least {target}")
        except Blocked as exc:
            reasons.append(str(exc))
    try:
        composite = probe_devcontainer(root, target)
        if composite:
            builders.append(composite)
    except Blocked as exc:
        reasons.append(str(exc))
    if not builders:
        reasons.append("builder inventory is empty")
    return builders, sources, reasons


def default_head(repo):
    branch = gh_json(f"repos/{repo}")["default_branch"]
    if not isinstance(branch, str) or not re.fullmatch(r"[A-Za-z0-9._/-]+", branch):
        raise ValueError("unsupported default branch name")
    return gh_json(f"repos/{repo}/commits/{branch}")["sha"]


def make_plan(root, repo):
    inv = inventory(root)
    current = stable(next(item["directives"]["go"] for item in inv["modules"] if item["path"] == "go.work"))
    plan = {"schema": 1, "status": "blocked", "repo": repo, "current": current,
            "target": None, "reason": "", "builders": [], **inv,
            "base_sha": run(["git", "rev-parse", "HEAD"], cwd=root)}
    try:
        if run(["git", "status", "--porcelain", "--untracked-files=no"], cwd=root):
            raise ValueError("plan requires a clean tracked checkout")
        if default_head(repo) != plan["base_sha"]:
            raise Blocked("checkout is not the current default-branch HEAD")
        with urllib.request.urlopen("https://go.dev/dl/?mode=json&include=all", timeout=60) as response:
            releases = json.load(response)
        target = latest_patch(current, releases)
        plan["target"] = target
        for item in inv["modules"]:
            for value in item["directives"].values():
                if version(stable(value)) > version(target):
                    raise Blocked(f"{item['path']} already requests {value}; refusing downgrade")
        if version(inv["recipe_version"]) > version(target):
            raise Blocked("API generator version is newer than target; refusing downgrade")
        proposed_dev = edit_devcontainer(inv["snapshots"][DEVCONFIG], target)
        overlaps = open_upgrades(repo)
        plan["overlapping_prs"] = overlaps
        if overlaps:
            raise Blocked("waiting for existing compiler upgrade PR(s): " + ", ".join(str(item["number"]) for item in overlaps))
        if (target == current and all(stable(value) == target for item in inv["modules"] for value in item["directives"].values())
                and inv["recipe_version"] == target and proposed_dev == inv["snapshots"][DEVCONFIG]):
            plan.update(status="current", reason=f"all tracked Go directives already use {target}")
            return plan
        builders, sources, reasons = builder_readiness(root, repo, target)
        plan.update(builders=builders, release_configs=sources)
        if reasons:
            raise Blocked("; ".join(reasons))
        plan.update(status="ready", reason=f"official Go {target} and all discovered builders are ready")
    except Blocked as exc:
        plan["reason"] = str(exc)
    return plan


def load_plan(path, root):
    plan = json.loads(Path(path).read_text())
    if plan.get("schema") != 1 or plan.get("status") != "ready" or plan.get("repo") != "Azure/ARO-HCP":
        raise ValueError("apply/check/recheck require a ready schema-1 Azure/ARO-HCP plan")
    current, target = version(plan["current"]), version(plan["target"])
    if target[:2] != current[:2] or target < current:
        raise ValueError("plan attempts a minor jump or downgrade")
    base = plan["base_sha"]
    if not isinstance(base, str) or not re.fullmatch(r"[0-9a-f]{40}", base):
        raise ValueError("plan base SHA is invalid")
    head = run(["git", "rev-parse", "HEAD"], cwd=root)
    if head != base:
        parents = run(["git", "rev-list", "--parents", "-n", "1", "HEAD"], cwd=root).split()
        if parents != [head, base]:
            raise ValueError("checkout must be plan base or its single local upgrade commit")
    # JSON never controls write paths: derive all paths afresh from git.
    inv = inventory(root)
    paths = {item["path"] for item in inv["modules"]}
    if paths != {item["path"] for item in plan["modules"]} or inv["files"] != plan["files"]:
        raise ValueError("tracked inventory changed since plan")
    expected_snapshots = set(inv["snapshots"])
    if set(plan["snapshots"]) != expected_snapshots:
        raise ValueError("plan snapshot inventory does not match checkout")
    for name, original in plan["snapshots"].items():
        actual = run(["git", "show", f"{base}:{name}"], cwd=root)
        if actual != original.rstrip("\n"):
            raise ValueError(f"plan snapshot differs from committed source: {name}")
    for item in plan["modules"]:
        if item["directives"] != directives(plan["snapshots"][item["path"]]):
            raise ValueError("plan directives differ from source snapshot")
        if any(version(stable(value)) > target for value in item["directives"].values()):
            raise ValueError("plan would downgrade an existing module directive")
    if stable(directives(plan["snapshots"]["go.work"])["go"]) != plan["current"]:
        raise ValueError("plan source version differs from committed workspace")
    return plan, inv


def installed_go(target):
    actual = run(["go", "env", "GOVERSION"], env=go_env())
    if actual != "go" + target:
        raise ValueError(f"installed compiler is {actual}, expected go{target} with GOTOOLCHAIN=local")


def apply_plan(root, path):
    plan, inv = load_plan(path, root)
    target = plan["target"]
    installed_go(target)
    if run(["git", "rev-parse", "HEAD"], cwd=root) != plan["base_sha"]:
        raise ValueError("apply requires the original plan base checkout")
    for name, text in inv["snapshots"].items():
        if text != plan["snapshots"][name]:
            raise ValueError(f"checkout changed since plan: {name}")
    if run(["git", "diff", "HEAD", "--name-only"], cwd=root):
        raise ValueError("apply requires unchanged tracked checkout")
    for item in inv["modules"]:
        name = item["path"]
        args = ["go", "work" if name.endswith("go.work") else "mod", "edit", f"-go={target}"]
        if "toolchain" in item["directives"]:
            args.append(f"-toolchain=go{target}")
        args.append(str(safe_file(root, name)))
        run(args, cwd=root, env=go_env())
    recipe = safe_file(root, "api/Makefile")
    recipe.write_text(RECIPE.sub(lambda m: m[1] + target + m[3], recipe.read_text()))
    devconfig = safe_file(root, DEVCONFIG)
    devconfig.write_text(edit_devcontainer(devconfig.read_text(), target))
    print(f"Updated {len(inv['modules'])} tracked Go manifests and API generator recipe to {target}; dependency checksums untouched")


def manifest_semantics(data):
    """Compare Go's parsed directives, not their single/block textual layout."""
    data = {key: value for key, value in data.items() if key not in ("Go", "Toolchain")}
    if data.get("Require"):
        data["Require"] = [{key: value for key, value in item.items() if key != "Indirect"}
                           for item in data["Require"]]

    def canonical(value):
        if isinstance(value, dict):
            return {key: canonical(child) for key, child in value.items()}
        if isinstance(value, list):
            return sorted((canonical(child) for child in value), key=lambda item: json.dumps(item, sort_keys=True))
        return value

    return canonical(data)


def dependency_content(text, workspace=False):
    """Use the installed target Go parser without resolving/downloading deps."""
    with tempfile.TemporaryDirectory(prefix="aro-hcp-go-manifest-") as temp:
        path = Path(temp, "go.work" if workspace else "go.mod")
        path.write_text(text)
        data = json.loads(run(["go", "work" if workspace else "mod", "edit", "-json", str(path)],
                              cwd=temp, env=go_env()))
    return manifest_semantics(data)


def check_plan(root, path):
    plan, inv = load_plan(path, root)
    target = plan["target"]
    installed_go(target)
    manifests = {item["path"] for item in inv["modules"]}
    original_manifests = {item["path"]: item for item in plan["modules"]}
    for item in inv["modules"]:
        expected = {"go": target}
        if "toolchain" in original_manifests[item["path"]]["directives"]:
            expected["toolchain"] = target
        if item["directives"] != expected:
            raise ValueError(f"unexpected target directives in {item['path']}: {item['directives']}")
        workspace = PurePosixPath(item["path"]).name == "go.work"
        if dependency_content(inv["snapshots"][item["path"]], workspace) != dependency_content(plan["snapshots"][item["path"]], workspace):
            raise ValueError(f"unrelated dependency/module changes in {item['path']}")
    recipe = plan["snapshots"]["api/Makefile"]
    expected_recipe = RECIPE.sub(lambda m: m[1] + target + m[3], recipe)
    if inv["snapshots"]["api/Makefile"] != expected_recipe:
        raise ValueError("API generator recipe has unrelated changes")
    if inv["snapshots"][DEVCONFIG] != edit_devcontainer(plan["snapshots"][DEVCONFIG], target):
        raise ValueError("devcontainer has unrelated configuration changes")
    changed = run(["git", "diff", plan["base_sha"], "--name-only", "-z"], cwd=root).split("\0")
    for name in filter(None, changed):
        if name in manifests or name in ("api/Makefile", DEVCONFIG):
            continue
        if name in inv["snapshots"]:
            # Excluded generated/tool manifests and dependency checksums are
            # protected snapshots, not compiler upgrade targets.
            if inv["snapshots"][name] != plan["snapshots"][name]:
                raise ValueError(f"unexpected protected manifest/checksum changes in {name}")
            continue
        local = safe_file(root, name)
        if name.endswith(".go") and re.search(r"^// Code generated .* DO NOT EDIT\.", local.read_text(), re.MULTILINE):
            old = run(["git", "show", f"{plan['base_sha']}:{name}"], cwd=root)
            if re.search(r"^// Code generated .* DO NOT EDIT\.", old, re.MULTILINE):
                continue
        raise ValueError(f"unexpected generated change: {name}")
    # The caller puts plan/logs outside the checkout; never waive unknown outputs.
    untracked = run(["git", "ls-files", "--others", "--exclude-standard", "-z"], cwd=root)
    if untracked:
        raise ValueError("unexpected untracked files after generation: " + untracked.replace("\0", ", "))
    print(f"Checked Go {target}, complete tracked module inventory, generator recipe and generated diff scope")


def draft_notifications_safe():
    """Fail closed if Prow can request human reviewers for a draft."""
    paths = ["core-services/prow/02_config/_plugins.yaml",
             "core-services/prow/02_config/Azure/_pluginconfig.yaml",
             "core-services/prow/02_config/Azure/ARO-HCP/_pluginconfig.yaml"]
    configs = []
    release_sha = default_head("openshift/release")
    for path in paths:
        content = gh_json(f"repos/openshift/release/contents/{path}?ref={release_sha}")
        if content.get("encoding") != "base64":
            raise ValueError("unsupported Prow config encoding")
        configs.append(yaml.safe_load(base64.b64decode(content["content"]).decode()))
    if configs[0].get("blunderbuss", {}).get("ignore_drafts") is not True:
        raise Blocked("Prow blunderbuss does not prove draft reviewer notifications disabled")

    def overridden(value):
        if isinstance(value, dict):
            return "blunderbuss" in value or any(overridden(child) for child in value.values())
        if isinstance(value, list):
            return any(overridden(child) for child in value)
        return False

    if any(overridden(config) for config in configs[1:]):
        raise Blocked("lower-level Prow blunderbuss override requires safety review before draft publication")


def recheck_plan(root, path, repo):
    plan, _ = load_plan(path, root)
    if repo != plan["repo"]:
        raise ValueError("recheck repository differs from plan")
    check_plan(root, path)
    if default_head(repo) != plan["base_sha"]:
        raise Blocked("default branch moved since plan; refusing publication")
    overlaps = open_upgrades(repo)
    if overlaps:
        raise Blocked("another compiler upgrade PR is open; refusing publication: " + ", ".join(str(item["number"]) for item in overlaps))
    builders, _, reasons = builder_readiness(root, repo, plan["target"])
    if reasons:
        raise Blocked("; ".join(reasons))
    if {item["image"] for item in builders} != {item["image"] for item in plan["builders"]}:
        raise Blocked("builder inventory changed since plan; regenerate plan")
    draft_notifications_safe()
    print(f"Publication recheck passed for Go {plan['target']}: default branch unchanged, no upgrade PR overlap, builders ready")


def emit(plan, output):
    Path(output).write_text(json.dumps(plan, indent=2, sort_keys=True) + "\n")
    print(f"Go toolchain {plan['status']}: current={plan['current']} target={plan['target'] or 'unknown'}; {plan['reason']}")
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
            stream.write(f"ready={str(plan['status'] == 'ready').lower()}\ntarget={plan['target'] or ''}\nstatus={plan['status']}\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    plan = commands.add_parser("plan")
    plan.add_argument("--repo", required=True)
    plan.add_argument("--output", required=True, help="write plan outside checkout to avoid untracked generation outputs")
    for command in ("apply", "check", "recheck"):
        sub = commands.add_parser(command)
        sub.add_argument("--plan", required=True)
        if command == "recheck":
            sub.add_argument("--repo", required=True)
    args = parser.parse_args()
    root = Path(run(["git", "rev-parse", "--show-toplevel"])).resolve()
    try:
        if args.command == "plan":
            emit(make_plan(root, args.repo), args.output)
        elif args.command == "apply":
            apply_plan(root, args.plan)
        elif args.command == "check":
            check_plan(root, args.plan)
        else:
            recheck_plan(root, args.plan, args.repo)
    except Blocked as exc:
        print(f"Go toolchain blocked: {exc}", file=sys.stderr)
        if args.command == "recheck":
            deferred = json.loads(Path(args.plan).read_text())
            deferred.update(status="blocked", reason=str(exc))
            Path(args.plan).write_text(json.dumps(deferred, indent=2, sort_keys=True) + "\n")
        return 3
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as exc:
        detail = exc.stderr if isinstance(exc, subprocess.CalledProcessError) else str(exc)
        print(f"Go toolchain error: {detail}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
