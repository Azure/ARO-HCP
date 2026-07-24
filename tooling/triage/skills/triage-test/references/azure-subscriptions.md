# Azure Subscriptions and Cluster Access

Scope: dev, pers, cspr, ci environments only.

## Subscriptions

ARO Hosted Control Planes (EA Subscription 1) — pers mgmt/svc clusters (pers-usw3*-mgmt-1, pers-usw3*-svc), cspr clusters (cspr-westus3-mgmt-1, cspr-westus3-svc)
ARO HCP E2E Infrastructure (EA Subscription) — ci mgmt/svc clusters (ci01-j*-mgmt-1, ci01-j*-svc), ephemeral
ARO HCP E2E Hosted Clusters (EA Subscription) — e2e hosted cluster Azure resources (VNets, Key Vaults, private endpoints)
ARO HCP E2E Hosted Clusters 2 (EA Subscription) — overflow for hosted cluster resources

All accessed with @redhat.com account, largely read/write.

int — TBD
stage — TBD
prod — TBD

## Cluster Access

Personal dev mgmt:
  az account set -s "ARO Hosted Control Planes (EA Subscription 1)"
  az aks get-credentials --resource-group hcp-underlay-pers-usw3<user>-mgmt-1 --name pers-usw3<user>-mgmt-1
  kubelogin convert-kubeconfig -l azurecli

CSPR mgmt:
  az aks get-credentials --subscription "ARO Hosted Control Planes (EA Subscription 1)" --resource-group hcp-underlay-cspr-westus3-mgmt-1 --name cspr-westus3-mgmt-1 --context cspr-mgmt
  kubelogin convert-kubeconfig --context cspr-mgmt -l azurecli

CI mgmt:
  az aks get-credentials --subscription "ARO HCP E2E Infrastructure (EA Subscription)" --resource-group hcp-underlay-ci01-j<id>-mgmt-1 --name ci01-j<id>-mgmt-1
  kubelogin convert-kubeconfig -l azurecli
  To find j<id>: az aks list --subscription "ARO HCP E2E Infrastructure (EA Subscription)" -o table

Via hcpctl (when available):
  hcpctl mc breakglass <mc-name>  — writes kubeconfig to temp file, export KUBECONFIG=<path>
  hcpctl sc breakglass <svc-name> — same pattern

int — TBD
stage — TBD
prod — TBD

## Kusto

Dev environments only. Production uses separate clusters — TBD.

Cluster: hcp-dev-us-2.eastus2.kusto.windows.net
Region: eastus2
Databases: HostedControlPlaneLogs, ServiceLogs
Covers: dev, ci, pers, cspr

Config file for kusto-query skill:
  region: eastus2
  kusto:
    name: hcp-dev-us-2
    hostedControlPlaneLogsDatabase: HostedControlPlaneLogs
    serviceLogsDatabase: ServiceLogs
