#!/usr/bin/env bash
# Copyright 2026 Microsoft Corporation.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# This script runs a local Grafana container wired to the Prometheus endpoints of
# your personal-dev Azure Monitor Workspaces (AMW). Run "make local-grafana-help"
# for more details.

# Usage: make local-grafana-[start|stop|status|help]

set -euo pipefail
umask 077

GRAFANA_VERSION="${GRAFANA_VERSION:-12.4.9}"
GRAFANA_PORT="${GRAFANA_PORT:-3000}"
CONTAINER_NAME="${CONTAINER_NAME:-aro-hcp-grafana}"
DEPLOY_ENV="${DEPLOY_ENV:-pers}"

GRAFANA_ANONYMOUS_ENABLED="${GRAFANA_ANONYMOUS_ENABLED:-true}"
GRAFANA_ADMIN_USER="${GRAFANA_ADMIN_USER:-admin}"
GRAFANA_ADMIN_PASSWORD="${GRAFANA_ADMIN_PASSWORD:-admin}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="${REPO_ROOT}/hack/.local-grafana"

PROMETHEUS_RESOURCE="https://prometheus.monitor.azure.com"
MONITORING_DATA_READER="Monitoring Data Reader"

err() { echo "ERROR: $*" >&2; }

get_container_runtime() {
    if command -v podman >/dev/null 2>&1; then
        echo "podman"
    elif command -v docker >/dev/null 2>&1; then
        echo "docker"
    else
        err "podman and docker not found. Ensure at least one is installed."
        exit 1
    fi
}

check_tools() {
    local tool
    for tool in az curl yq; do
        if ! command -v "${tool}" >/dev/null 2>&1; then
            err "${tool} is required but not found"
            exit 1
        fi
    done
}

# Resolve REGION_RG, SVC_WORKSPACE_NAME, HCP_WORKSPACE_NAME from config via templatize
# using the same dev-settings / dev-environment the make targets use.
resolve_config() {
    local templatize
    templatize="${REPO_ROOT}/tooling/templatize/templatize-$(uname -m)"
    if [[ ! -x "${templatize}" ]]; then
        err "templatize binary not found under ${REPO_ROOT}/tooling/templatize/"
        err "Run 'make templatize' to build it, or use 'make local-grafana-start'"
        exit 1
    fi

    local template rendered
    template="$(mktemp)"
    rendered="$(mktemp)"
    # Self-clear the trap after it fires
    trap 'rm -f "${template}" "${rendered}"; trap - RETURN' RETURN

    cat > "${template}" <<'TEMPLATE'
REGION_RG={{ .regionRG }}
SVC_WORKSPACE_NAME={{ .monitoring.svcWorkspaceName }}
HCP_WORKSPACE_NAME={{ .monitoring.hcpWorkspaceName }}
TEMPLATE

    "${templatize}" generate \
        --config-file "${REPO_ROOT}/config/config.yaml" \
        --dev-settings-file "${REPO_ROOT}/tooling/templatize/settings.yaml" \
        --dev-environment "${DEPLOY_ENV}" \
        --input "${template}" \
        --output "${rendered}"

    # shellcheck disable=SC1090
    source "${rendered}"

    if [[ -z "${REGION_RG:-}" || -z "${SVC_WORKSPACE_NAME:-}" || -z "${HCP_WORKSPACE_NAME:-}" ]]; then
        err "Failed to resolve config for DEPLOY_ENV=${DEPLOY_ENV}"
        exit 1
    fi

    echo "Resolved config for DEPLOY_ENV=${DEPLOY_ENV}:"
    echo "  REGION_RG=${REGION_RG}"
    echo "  SVC_WORKSPACE_NAME=${SVC_WORKSPACE_NAME}"
    echo "  HCP_WORKSPACE_NAME=${HCP_WORKSPACE_NAME}"
}

get_prometheus_endpoint() {
    local name="$1" rg="$2"
    az monitor account show \
        --name "${name}" \
        --resource-group "${rg}" \
        --query "metrics.prometheusQueryEndpoint" -o tsv 2>/dev/null
}

get_access_token() {
    az account get-access-token \
        --resource "${PROMETHEUS_RESOURCE}" \
        --query "accessToken" -o tsv 2>/dev/null
}

get_amw_resource_id() {
    local name="$1" rg="$2"
    az monitor account show \
        --name "${name}" \
        --resource-group "${rg}" \
        --query "id" -o tsv 2>/dev/null
}

# Verify the current user can actually query the AMW. If not (401/403),
# print the exact role-assignment command and fail.
check_amw_access() {
    local name="$1" rg="$2" endpoint="$3" token="$4"

    local http_code
    http_code=$(curl -s -o /dev/null -w '%{http_code}' \
        --connect-timeout 5 --max-time 10 \
        -H "Authorization: Bearer ${token}" \
        "${endpoint%/}/api/v1/query?query=up" || echo "000")

    if [[ "${http_code}" == "200" ]]; then
        return 0
    fi

    if [[ "${http_code}" == "401" || "${http_code}" == "403" ]]; then
        local amw_id principal
        amw_id="$(get_amw_resource_id "${name}" "${rg}")"
        principal="$(az ad signed-in-user show --query id -o tsv 2>/dev/null || true)"
        err "No query access to AMW '${name}' (HTTP ${http_code})."
        echo "Grant yourself '${MONITORING_DATA_READER}' on it with:" >&2
        echo "  az role assignment create \\" >&2
        echo "    --assignee ${principal:-<your-object-id>} \\" >&2
        echo "    --role \"${MONITORING_DATA_READER}\" \\" >&2
        echo "    --scope ${amw_id:-<amw-resource-id>}" >&2
        return 1
    fi

    err "Unexpected response querying AMW '${name}' (HTTP ${http_code}). Are you logged in (az login)?"
    return 1
}

# Generate Grafana dashboard config from observability.yaml.
# Folder paths './grafana-dashboards/X' map to the container mount '/var/lib/grafana/dashboards/X'.
generate_dashboards_config() {
    local observability_config="${REPO_ROOT}/observability/observability.yaml"
    local out="$1"

    if [[ ! -f "${observability_config}" ]]; then
        err "Cannot find ${observability_config}"
        exit 1
    fi

    # For each entry under grafana-dashboards.dashboardFolders, output a dashboard provider entry
    # to dashboards.yaml.
    # The container's path to the dashboard prepends the mount point, removes the './grafana-dashboards' prefix,
    # and removes a trailing slash.
    yq eval '
      {
        "apiVersion": 1,
        "providers": [
          .["grafana-dashboards"].dashboardFolders[] | {
            "name": .name,
            "folder": .name,
            "type": "file",
            "options": {
              "path": "/var/lib/grafana/dashboards" + (.path | sub("^\./grafana-dashboards", "") | sub("/$", ""))
            }
          }
        ]
      }
    ' "${observability_config}" > "${out}"
}

append_azure_monitor_datasource() {
    local out="$1"

    if [[ -z "${AZURE_CLIENT_ID:-}" || -z "${AZURE_CLIENT_SECRET:-}" ]]; then
        echo '"Azure Monitor" datasource skipped. Run "make local-grafana-help" for details to enable the "Azure Monitor" datasource.'
        return 0
    fi

    local tenant_id="${AZURE_TENANT_ID:-}" subscription_id="${AZURE_SUBSCRIPTION_ID:-}"
    if [[ -z "${tenant_id}" ]]; then
        tenant_id="$(az account show --query tenantId -o tsv 2>/dev/null || true)"
    fi
    if [[ -z "${subscription_id}" ]]; then
        subscription_id="$(az account show --query id -o tsv 2>/dev/null || true)"
    fi
    if [[ -z "${tenant_id}" || -z "${subscription_id}" ]]; then
        err "Azure Monitor datasource requested but tenant/subscription could not be resolved."
        err "Set AZURE_TENANT_ID and AZURE_SUBSCRIPTION_ID manually, or run 'az login'."
        return 1
    fi

    TENANT_ID="${tenant_id}" \
    CLIENT_ID="${AZURE_CLIENT_ID}" \
    SUBSCRIPTION_ID="${subscription_id}" \
    CLIENT_SECRET="${AZURE_CLIENT_SECRET}" \
    yq -i '.datasources += [{
      "name": "Azure Monitor",
      "uid": "azure-monitor-oob",
      "type": "grafana-azure-monitor-datasource",
      "jsonData": {
        "azureAuthType": "clientsecret",
        "cloudName": "azuremonitor",
        "tenantId": strenv(TENANT_ID),
        "clientId": strenv(CLIENT_ID),
        "subscriptionId": strenv(SUBSCRIPTION_ID)
      },
      "secureJsonData": {
        "clientSecret": strenv(CLIENT_SECRET)
      },
      "editable": true
    }]' "${out}"
}

# Generate the two AMW Prometheus datasources. Their names must match
# the datasource regexes:
# ^Managed_Prometheus_services-.*$ and ^Managed_Prometheus_hcps-.*$
generate_datasources_config() {
    local svc_endpoint="$1" hcp_endpoint="$2" token="$3" out="$4"

    cat > "${out}" <<YAML
apiVersion: 1
datasources:
  - name: Managed_Prometheus_${SVC_WORKSPACE_NAME}
    uid: aro-hcp-amw-svc
    type: prometheus
    access: proxy
    url: ${svc_endpoint}
    isDefault: true
    jsonData:
      httpMethod: POST
      httpHeaderName1: Authorization
      timeInterval: 60s
    secureJsonData: {}
    editable: true

  - name: Managed_Prometheus_${HCP_WORKSPACE_NAME}
    uid: aro-hcp-amw-hcp
    type: prometheus
    access: proxy
    url: ${hcp_endpoint}
    jsonData:
      httpMethod: POST
      httpHeaderName1: Authorization
      timeInterval: 60s
    secureJsonData: {}
    editable: true
YAML

    # Inject the bearer token via yq so the value is YAML-escaped regardless of
    # its contents, rather than interpolated raw into the heredoc above.
    BEARER="Bearer ${token}" yq -i '
      .datasources[0].secureJsonData.httpHeaderValue1 = strenv(BEARER) |
      .datasources[1].secureJsonData.httpHeaderValue1 = strenv(BEARER)
    ' "${out}"

    append_azure_monitor_datasource "${out}"
}

container_exists() {
    ${CONTAINER_RUNTIME} ps -a --format '{{.Names}}' | grep -Fxq "${CONTAINER_NAME}"
}

container_running() {
    ${CONTAINER_RUNTIME} ps --format '{{.Names}}' | grep -Fxq "${CONTAINER_NAME}"
}

stop_grafana() {
    echo "=== Stopping Grafana container (if it exists) ==="
    if container_exists; then
        echo "Stopping and removing ${CONTAINER_NAME}..."
        ${CONTAINER_RUNTIME} rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
        echo "Stopped."
    else
        echo "Container ${CONTAINER_NAME} is not running."
    fi

    echo "=== Removing state directory ==="
    rm -rf "${STATE_DIR}"
    echo "'${STATE_DIR}' removed."
}

start_grafana() {
    stop_grafana

    echo "=== Resolving pers environment config values ==="
    resolve_config

    echo "=== Getting AMW Prometheus endpoints ==="
    local svc_endpoint hcp_endpoint
    svc_endpoint="$(get_prometheus_endpoint "${SVC_WORKSPACE_NAME}" "${REGION_RG}")"
    hcp_endpoint="$(get_prometheus_endpoint "${HCP_WORKSPACE_NAME}" "${REGION_RG}")"
    if [[ -z "${svc_endpoint}" ]]; then
        err "Could not resolve SVC AMW endpoint for ${SVC_WORKSPACE_NAME} in ${REGION_RG}."
        err "Is the personal-dev region deployed and are you on the right subscription? (az login)"
        exit 1
    fi
    if [[ -z "${hcp_endpoint}" ]]; then
        err "Could not resolve HCP AMW endpoint for ${HCP_WORKSPACE_NAME} in ${REGION_RG}."
        err "Is the personal-dev region deployed and are you on the right subscription? (az login)"
        exit 1
    fi
    echo "SVC endpoint: ${svc_endpoint}"
    echo "HCP endpoint: ${hcp_endpoint}"

    echo "=== Getting Prometheus access token ==="
    local token
    token="$(get_access_token)"
    if [[ -z "${token}" ]]; then
        err "Could not get access token. Run 'az login' first."
        exit 1
    fi
    echo "Success."

    echo "=== Verifying AMW access ==="
    local access_ok=true
    check_amw_access "${SVC_WORKSPACE_NAME}" "${REGION_RG}" "${svc_endpoint}" "${token}" || access_ok=false
    check_amw_access "${HCP_WORKSPACE_NAME}" "${REGION_RG}" "${hcp_endpoint}" "${token}" || access_ok=false
    if [[ "${access_ok}" != "true" ]]; then
        err "Missing AMW access — grant the role(s) above and re-run."
        exit 1
    fi
    echo "Success."

    echo "=== Generating dashboard and datasource configs ==="
    local dashboards_dir="${STATE_DIR}/provisioning/dashboards"
    local datasources_dir="${STATE_DIR}/provisioning/datasources"
    mkdir -p "${dashboards_dir}" "${datasources_dir}"
    chmod 700 "${STATE_DIR}" # restrict perms since the datasource file embeds a bearer token

    generate_dashboards_config "${dashboards_dir}/dashboards.yaml"
    echo "${dashboards_dir}/dashboards.yaml generated."
    generate_datasources_config "${svc_endpoint}" "${hcp_endpoint}" "${token}" "${datasources_dir}/datasources.yaml"
    echo "${datasources_dir}/datasources.yaml generated."

    # Grafana runs as a non-root user and must read these two mounted directories
    # (the 'umask 077' above makes them unreadable).
    # This makes them readable while still keeping the bearer token safe.
    chmod 755 "${dashboards_dir}" "${datasources_dir}"
    chmod 644 "${dashboards_dir}"/*.yaml "${datasources_dir}"/*.yaml

    echo "=== Starting Grafana container ==="
    echo "Starting Grafana ${GRAFANA_VERSION} on port ${GRAFANA_PORT} via ${CONTAINER_RUNTIME}..."

    # Note on the '--security-opt label=disable' flag:
    #
    # On a host with SELinux (Security-Enhanced Linux) in enforcing mode (such as Fedora),
    # a container cannot read a mounted host directory by default. The kernel allows the
    # read only if the directory and its contents have a container-access label. SELinux
    # stores this label in the 'security.selinux' xattr in the inode's metadata.
    #
    # '--security-opt label=disable' disables SELinux confinement for this one container.
    # It also avoids a relabel of any host files. A relabel would outlive the container.
    # A relabel could also break access for other consumers of those files.
    #
    # On a host without SELinux, this option does nothing.
    ${CONTAINER_RUNTIME} run -d \
        --name "${CONTAINER_NAME}" \
        -p "127.0.0.1:${GRAFANA_PORT}:3000" \
        --security-opt label=disable \
        -e GF_AUTH_ANONYMOUS_ENABLED="${GRAFANA_ANONYMOUS_ENABLED}" \
        -e GF_AUTH_ANONYMOUS_ORG_ROLE=Viewer \
        -e GF_SECURITY_ADMIN_USER="${GRAFANA_ADMIN_USER}" \
        -e GF_SECURITY_ADMIN_PASSWORD="${GRAFANA_ADMIN_PASSWORD}" \
        -e GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH=/var/lib/grafana/dashboards/sre/user-journey/mgmt-cluster-triage.json \
        -v "${REPO_ROOT}/observability/grafana-dashboards:/var/lib/grafana/dashboards:ro" \
        -v "${dashboards_dir}:/etc/grafana/provisioning/dashboards:ro" \
        -v "${datasources_dir}:/etc/grafana/provisioning/datasources:ro" \
        "grafana/grafana:${GRAFANA_VERSION}" >/dev/null

    local datasources
    datasources="$(yq eval '.datasources[].name | "  - " + .' "${datasources_dir}/datasources.yaml")"

    local access_note
    if [[ "${GRAFANA_ANONYMOUS_ENABLED}" == "true" ]]; then
        access_note="Anonymous access is read-only (Viewer). Log in as ${GRAFANA_ADMIN_USER} to edit."
    else
        access_note="Anonymous access is disabled. Log in as ${GRAFANA_ADMIN_USER} to view and edit."
    fi

    cat <<EOF

Grafana is running at: http://localhost:${GRAFANA_PORT}
${access_note}
Datasources:
${datasources}

NOTE: the Azure token expires in ~1h. Run 'make local-grafana-start' to refresh it
      (this recreates the container from scratch; dashboards edited or created
      through the UI are NOT persisted after container teardown).
EOF
}

status_grafana() {
    if container_running; then
        echo "Container ${CONTAINER_NAME} is running."
        echo "  URL: http://localhost:${GRAFANA_PORT}"
        ${CONTAINER_RUNTIME} ps --filter "name=${CONTAINER_NAME}" --format "  Status: {{.Status}}"
        echo "Token expires after ~1hr. Run 'make local-grafana-start' to restart the container and refresh the token."
    elif container_exists; then
        echo "Container ${CONTAINER_NAME} exists but is not running."
        ${CONTAINER_RUNTIME} ps -a --filter "name=${CONTAINER_NAME}" --format "  Status: {{.Status}}"
        echo "Token expires after ~1hr. Run 'make local-grafana-start' to restart the container and refresh the token."
    else
        echo "Container ${CONTAINER_NAME} does not exist."
    fi
}

usage_grafana() {
    cat <<EOF
==============================================================================
Usage: make local-grafana-[start|stop|status|help]

Commands:
  start     Start local Grafana container. Re-run to refresh the ~1h AMW token
            (this recreates the container from scratch; dashboards edited or created
            through the UI are NOT persisted after container teardown).
  stop      Stop and remove the container and generated state.
  status    Show container status.
  help      Show help and usage.

This script starts a local Grafana container that connects to your personal dev environment's
Azure Monitor Workspace (AMW) Prometheus endpoints.

Prerequisites:
  - podman or docker
  - az CLI, logged into the "ARO Hosted Control Planes (EA Subscription 1)" subscription:
        az login
  - an existing personal-dev region so the AMWs exist:
        make personal-dev-env
  - optional: an HCP deployed:
        ARO_E2E_SKIP_CLEANUP=true make e2e-local/run-test TEST_NAME="ARO-HCP HyperShift Presubmit should create a cluster and nodepool to completion"
  - optional: A service principal with "Monitoring Reader" RBAC on the subscription in order to view
    the "Azure Monitor" datasource:
        az ad sp create-for-rbac --name "aro-hcp-local-grafana-$(whoami)" --role "Monitoring Reader" --scopes "/subscriptions/<subscription-id>"

        NOTE: There is a known bug for versions < 2.87.0 of the az CLI when running this command with Python
        3.14 (https://github.com/azure/azure-cli/issues/32832). To remediate, either update az CLI to 2.87+,
        run with a different Python version, run in Azure Cloud shell, or create the sp/secret and link to
        the subscription manually in Azure Portal. The sp is not idempotent so only run this command once,
        and delete the sp when you are no longer using it.

        The output from az CLI should contain the following:
        {
            "appId":    "...",   // -> AZURE_CLIENT_ID
            "password": "...",   // -> AZURE_CLIENT_SECRET
        }

        Set these environment variables:
        export AZURE_CLIENT_ID=<appId>
        export AZURE_CLIENT_SECRET=<password>

        Run:
        make local-grafana-start

        Finally, remove all url parameters before refreshing any dashboards to clear cached datasource values.

Environment variables:
  DEPLOY_ENV                 Target dev environment (default: pers)
  GRAFANA_PORT               Local port (default: 3000)
  GRAFANA_VERSION            Grafana image tag (default: 12.4.9)
  CONTAINER_NAME             Container name (default: aro-hcp-grafana)
  GRAFANA_ANONYMOUS_ENABLED  Allow anonymous read-only (Viewer) access (default: true)
  GRAFANA_ADMIN_USER         Grafana admin username (default: admin)
  GRAFANA_ADMIN_PASSWORD     Grafana admin password (default: admin)
  AZURE_CLIENT_ID            Service principal appId for the Azure Monitor datasource
  AZURE_CLIENT_SECRET        Service principal secret for the Azure Monitor datasource
  AZURE_TENANT_ID            Tenant for the Azure Monitor datasource (default: tenant read from 'az account')
  AZURE_SUBSCRIPTION_ID      Subscription for the Azure Monitor datasource (default: subscription read from 'az account')
==============================================================================
EOF
}

case "${1:-help}" in
    start)
        check_tools
        CONTAINER_RUNTIME="$(get_container_runtime)"
        start_grafana
        ;;
    stop)
        CONTAINER_RUNTIME="$(get_container_runtime)"
        stop_grafana
        ;;
    status)
        CONTAINER_RUNTIME="$(get_container_runtime)"
        status_grafana
        ;;
    -h|--help|help) usage_grafana ;;
    *)       usage_grafana; exit 1 ;;
esac