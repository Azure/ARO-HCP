# Grafana VPN Access

## Overview

Azure Managed Grafana instances in staging, integration, and production environments can be configured with `publicNetworkAccess: Disabled`. On its own, this only removes the public internet path — it does **not** grant MSFT Corp VPN users access. A Private Endpoint (with private DNS and VPN routing) and/or the `crossTenantSecurityGroup` tag must also be in place for authorized users to reach Grafana once public access is disabled; otherwise everyone, including VPN users, is locked out. Dev environment Grafana instances are publicly accessible.

## Prerequisites

- MSFT Corp VPN client installed and configured
- Azure AD account with an appropriate Grafana role assignment (Viewer, Contributor, or Admin)
- Active VPN connection to the MSFT corporate network

## Procedure

1. Connect to the MSFT Corp VPN
2. Look up the Grafana URL for your target environment — the endpoint includes an Azure-generated hash suffix and region code (e.g. `arohcp-dev-c9g7a4fjanb0c4gc.wus3.grafana.azure.com`) that cannot be guessed from the instance name alone. Retrieve it via the Azure Portal or `az grafana show --name <grafana-name> --resource-group <rg> --query properties.endpoint`
3. Authenticate with your Azure AD credentials

## Troubleshooting

| Symptom | Possible Cause | Resolution |
|---------|---------------|------------|
| Cannot reach Grafana URL (timeout/connection refused) | VPN not connected, or connected to a guest/partner network instead of Corp VPN | Connect to MSFT Corp VPN and retry |
| "Public Network Address Denied" page | Public network access is disabled and the request did not arrive via the private path (PE not yet provisioned, or DNS not resolving to the private IP) | Verify VPN connectivity to the private endpoint's DNS zone; escalate to the platform team if the PE/DNS path is not configured |
| 403 Forbidden (page loads, login succeeds) | The request reached Grafana — this is an authorization issue, not a network issue. The account/tenant lacks a Grafana role assignment | Request a Grafana role assignment (Viewer, Contributor, or Admin) for your account |
| Dev environment works but staging/int/prod does not | Dev has public access enabled; staging/int/prod may require VPN | Connect to VPN for non-dev environments |
| VPN connected but still cannot access | DNS resolution issue | Flush DNS cache and retry; verify VPN routes include Azure endpoints |

## Related Documentation

- [Grafana Dashboards](../grafana-dashboards.md)
- [Alert Verification](../alert-verification.md)
- [Monitoring](../monitoring.md)
- [Network Security](../network-security.md)
