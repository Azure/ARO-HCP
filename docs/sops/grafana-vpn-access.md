# Grafana VPN Access

## Overview

Azure Managed Grafana instances in staging, integration, and production environments are configured with `publicNetworkAccess: Disabled`. This restricts access to users connected to the MSFT Corp VPN. Dev environment Grafana instances remain publicly accessible.

## Prerequisites

- MSFT Corp VPN client installed and configured
- Azure AD account with an appropriate Grafana role assignment (Viewer, Contributor, or Admin)
- Active VPN connection to the MSFT corporate network

## Procedure

1. Connect to the MSFT Corp VPN
2. Navigate to the Grafana URL for your target environment (e.g. `https://arohcp-<env>-<id>.grafana.azure.com/`)
3. Authenticate with your Azure AD credentials

## Troubleshooting

| Symptom | Possible Cause | Resolution |
|---------|---------------|------------|
| Cannot reach Grafana URL (timeout/connection refused) | VPN not connected | Connect to MSFT Corp VPN and retry |
| 403 Forbidden or network error | Connected to guest/partner network instead of Corp VPN | Switch to the MSFT Corp VPN |
| Dev environment works but staging/prod does not | Dev has public access enabled; staging/int/prod require VPN | Connect to VPN for non-dev environments |
| VPN connected but still cannot access | DNS resolution issue | Flush DNS cache and retry; verify VPN routes include Azure endpoints |

## Related Documentation

- [Grafana Dashboards](../grafana-dashboards.md)
- [Alert Verification](../alert-verification.md)
- [Monitoring](../monitoring.md)
- [Network Security](../network-security.md)
