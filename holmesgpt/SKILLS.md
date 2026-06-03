# HolmesGPT Skills for ARO-HCP

## What are Skills?

Skills are step-by-step troubleshooting guides that [HolmesGPT](https://holmesgpt.dev/) follows when investigating issues. When a user asks Holmes a question, Holmes matches relevant skills based on the skill's `description` field and follows the workflow steps defined in the skill.

Skills are `SKILL.md` files with YAML frontmatter and a markdown body. See the [HolmesGPT skills reference](https://holmesgpt.dev/dev/reference/skills/) for the full specification.

## Skill Locations

Each investigation scope has its own skills directory. Skills are scope-specific — a skill in one scope cannot access resources in another scope.

| Scope | Directory | Deployed to | What it can access |
|-------|-----------|-------------|-------------------|
| **serviceplane** | `holmesgpt/deploy-svc/skills/` | Service cluster Holmes | Pods, logs, events in `aro-hcp`, `clusters-service`, `maestro` namespaces |
| **controlplane** | `holmesgpt/deploy-mgmt/skills/` | Management cluster Holmes | HostedCluster, NodePool, ManifestWork, ManagedCluster, CAPI resources, HCP namespace pods |
| **dataplane** | `holmesgpt/deploy-mgmt/skills-dataplane/` | Ephemeral pod on management cluster | Customer cluster resources via CSR-signed kubeconfig (nodes, pods, operators) |

## Adding a New Skill

### 1. Create the skill file

Create a directory under the appropriate scope location with a `SKILL.md` file:

```
holmesgpt/deploy-svc/skills/my-new-skill/SKILL.md        # serviceplane
holmesgpt/deploy-mgmt/skills/my-new-skill/SKILL.md       # controlplane
holmesgpt/deploy-mgmt/skills-dataplane/my-new-skill/SKILL.md  # dataplane
```

### 2. Write the skill

```yaml
---
name: my-new-skill
description: One-line description — Holmes uses this to match skills to user questions. Be specific.
---

## Goal
What this skill diagnoses.

## Important Instructions
- Do NOT check `kubectl config current-context` — you are running in-cluster.
- Do NOT ask the user for kubeconfig or cluster access.
- When checking logs, use `--tail=100`.
- Keep output concise.

## Workflow

### Step 1: Check something
* Run: `kubectl get pods -n <namespace>`
* Look for: ...

### Step 2: Check something else
* Run: `kubectl logs <pod> -n <namespace> --tail=100 | grep <pattern>`
* Look for: ...

## Synthesize Findings
* If X → likely cause is Y
* If A → escalate to <other scope>

## Recommended Remediation Steps
* ...
```

### 3. Register in the Helm ConfigMap

**For serviceplane or controlplane skills:**

Edit the corresponding `templates/configmap.yaml` to add the skill to the `holmesgpt-skills` ConfigMap:

```yaml
# In holmesgpt/deploy-svc/templates/configmap.yaml (serviceplane)
# or holmesgpt/deploy-mgmt/templates/configmap.yaml (controlplane)
data:
  my-new-skill.md: |
{{ .Files.Get "skills/my-new-skill/SKILL.md" | indent 4 }}
```

Add a volume mount in the corresponding `templates/deployment.yaml`:

```yaml
- name: holmes-skills
  mountPath: /etc/holmes/skills/my-new-skill/SKILL.md
  subPath: my-new-skill.md
  readOnly: true
```

**For dataplane skills:**

Edit `holmesgpt/deploy-mgmt/templates/configmap.yaml` to add it to the `holmesgpt-dataplane-config` ConfigMap:

```yaml
  skill-my-new-skill.md: |
{{ .Files.Get "skills-dataplane/my-new-skill/SKILL.md" | indent 4 }}
```

Add a volume mount in `admin/server/holmes/pod.go`:

```go
{Name: "dataplane-config", MountPath: "/etc/holmes/skills/my-new-skill/SKILL.md", SubPath: "skill-my-new-skill.md", ReadOnly: true},
```

### 4. Materialize and test

```bash
cd config && make materialize    # updates Helm fixtures
go build ./admin/server/...      # if dataplane skill, verifies pod.go compiles
go test ./admin/server/holmes/... -race

# Deploy and test
SCOPE=<scope> demo/test-holmes-investigate.sh "<question that should trigger your skill>"
```

## Key Guidelines for Writing Skills

1. **Be specific in the `description`** — Holmes matches skills to questions using this field. Vague descriptions won't trigger.

2. **Scope boundaries** — serviceplane skills cannot access ManifestWork, HostedCluster, or any management cluster CRDs. Add a note in Important Instructions.

3. **Use `--tail=100`** — unbounded log fetching can exceed the LLM's context window (272K tokens).

4. **Fetch from ONE pod per deployment** — don't fetch logs from all replicas.

5. **Don't check `kubectl config current-context`** — in-cluster SA doesn't have a kubeconfig context. kubectl works automatically.

6. **Include remediation steps** — tell the user exactly what command to run to fix the issue.

7. **Reference the existing runbooks** — check `docs/ops/` for existing procedures that can inform your skill workflow.

## Existing Skills

| Skill | Scope | Purpose |
|-------|-------|---------|
| `hcp-creation-serviceplane` | serviceplane | Diagnose HCP creation stuck/failed — checks frontend, backend, CS, Maestro |
| `hcp-creation-controlplane` | controlplane | Diagnose HCP creation stuck — checks HostedCluster, control plane pods, NodePool, CAPI |
| `hcp-creation-dataplane` | dataplane | Diagnose HCP creation data plane issues — checks nodes, operators, ClusterVersion |
| `hcp-deletion-serviceplane` | serviceplane | Diagnose HCP deletion stuck — checks backend operation, CS uninstalling, Maestro bundles |
| `hcp-deletion-controlplane` | controlplane | Diagnose HCP deletion stuck — checks finalizers on HostedCluster, ManagedCluster, CAPI resources |
| `hcp-deletion-dataplane` | dataplane | Diagnose HCP deletion data plane issues — checks node draining, stuck pods, PVCs |
