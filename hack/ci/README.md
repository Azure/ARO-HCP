# CI Workspace Overrides

The override builder accepts two independent, optional external Azure Monitor
workspace ARM IDs:

| Environment Variable | Config Key Under `monitoring` |
| --- | --- |
| `SVC_AMW_RESOURCE_ID` | `svcWorkspaceResourceId` |
| `HCP_AMW_RESOURCE_ID` | `hcpWorkspaceResourceId` |

Alternatively, CI supplies independent `SVC_AMW_LEASE` and `HCP_AMW_LEASE` names
from normal workflow-level Boskos leases. The builder resolves them through
`dev-infrastructure/openshift-ci/amw-pool.yaml`, or `AMW_POOL_CATALOG` when set.
Each entry must have a valid workspace ID, location, and matching `services` or
`hcps` purpose. Unknown leases and invalid entries fail provisioning; there is
no fallback to a fresh workspace. A direct ID and lease for the same purpose
are mutually exclusive. When leases are supplied, the two purposes must resolve
to different AMWs.

Unset or empty inputs add no workspace override. Personal validation uses
explicit IDs or the equivalent config override. Supply only ARM resource IDs,
never credentials or credential-bearing URLs. CI-operator owns lease renewal
and release through the end of the workflow, including its cleanup steps.

For a personal config override, set either or both keys under
`clouds.dev.environments.pers.defaults.monitoring` in your override YAML and use
the existing `OVERRIDE_CONFIG_FILE` mechanism. Setting these environment variables
alone does not change personal `make` targets that do not call this CI builder.

Provisioning and upgrade builds use the same helper and workflow leases. From-main
preserves the catalog at `${SHARED_DIR}/baseline-amw-pool.yaml` before checking out
the baseline revision. That revision must already contain external-AMW config and
lease-resolver support; the catalog alone cannot make an older baseline compatible.
Publish compatible source and images before enabling the companion release
workflow changes. Never advertise pool members that still have unleased writers.

Run the offline shell tests from the ARO-HCP root with Bash, yq v4, and jq:

```bash
bash hack/ci/build-config-override_test.sh
bash hack/ci/build-config-override_test.sh /path/to/openshift/release/worktree
```

The second form also exercises the release repository's duplicated provisioning
script. Cloud commands, credential reads, Git checkout, and deployment are mocked;
real YAML generation is converted to JSON and checked for default absence,
independent external IDs, catalog resolution and rejection, literal escaping,
baseline/upgrade preservation, and credential non-disclosure.

Release step refs retain basename-only `commands` values (for example,
`aro-hcp-provision-environment-commands.sh`), consistent with the step-registry
naming convention. The registry resolves that file relative to its ref directory;
it is not a repository-root script path. Tests use the full filesystem path only
when executing the script locally.
