# DNS Architecture in ARO HCP

## Overview

ARO HCP uses a dual DNS hierarchy approach with two distinct domain structures:

- **Service (SVC) Zone**: Internal Azure infrastructure hosting the Resource Provider and related services
- **Customer Experience (CX) Zone**: Customer-facing domains for Hosted Control Planes (HCPs)

ARO HCP operates as a regional service with completely independent instances per Azure region. Each regional deployment creates:

- A regional DNS zone under the SVC parent zone (e.g. `uksouth.{svcParentZoneName}`)
- A regional DNS zone under the CX parent zone (e.g. `uksouth.{cxParentZoneName}`)

These hierarchies have different management models:

- **SVC records** are managed at infrastructure buildout time during regional deployment
- **CX record / subzones** are dynamically managed at HCP provisioning time

## DNS Zone Structure

### Parent Zone Configuration

The DNS hierarchy is built on two parent zones defined in configuration:

- `dns.svcParentZoneName`: Parent zone for service infrastructure
- `dns.cxParentZoneName`: Parent zone for customer-facing domains

These parent zones are owned and managed by the ARO HCP team and are located in the global subscription (`global.subscription`) and global resource group (`global.rg`).

### Regional Zones

Regional zones are located in the regional subscription (`service.subscription`) and regional resource group (`regionRG`).

Regional DNS zones follow a consistent naming pattern:

```text
{dns.regionalSubdomain}.{dns.svcParentZoneName}
{dns.regionalSubdomain}.{dns.cxParentZoneName}
```

Examples:

- `uksouth.aro-hcp.azure.com`
- `uksouth.aroapp-hcp.io`

### Environment Differentiation

The staging environment shares the same parent zone as production and uses a modified naming convention to distinguish from production. This is achieved by adding the `staging` suffix to the regional subdomain configuration.

```yaml
config.yaml:
...
  dns:
    regionalSubdomain: "{{ .ctx.region }}staging"
```

Example: `uksouthstaging.aroapp-hcp.io`

### Zone Management and Deployment

DNS zones are created as part of the infrastructure deployment process:

- **Parent zones**: Managed in `global-infra.bicep`, delegation from the parent-parent zones is managed through EV2
- **Regional zones**: Created via `region.bicep` during regional deployment, delegation from parent zones is managed through `region.bicep`

## SVC DNS Zone

The Service DNS zone hosts internal Azure infrastructure components and follows the pattern `{dns.regionalSubdomain}.{dns.svcParentZoneName}`.

### Hosted Services

Currently, the SVC zone hosts:

- **Resource Provider Frontend**: `rp.{regionalSvcZone}`
- **Future**: Admin API endpoint, Backplane API endpoint, etc.

All SVC DNS records are created and managed through Bicep templates during infrastructure deployment.

### Certificate Namespace Usage

The SVC zone domain name serves as a namespace for internal client certificates used for service authentication, including:

- Event Grid authentication (Maestro)
- Other internal service-to-service authentication (Geneva Logs, Geneva Actions)

While not directly DNS-related, this provides consistent naming conventions across the service infrastructure.

### OIDC Integration

The SVC zone includes a dedicated OIDC subdomain structure:

- **Parent zone**: `oic.{dns.svcParentZoneName}`
- **Regional CNAMEs**: `{dns.regionalSubdomain}.oic.{dns.svcParentZoneName}`

Each regional CNAME points to an Azure Front Door endpoint that forwards traffic to the regional storage account containing OIDC artifacts for HCPs in that region.

### Access and Visibility

SVC zone records are internal to Azure infrastructure and not directly accessible by customers (with the OIDC endpoints being the exception). All customer interactions flow through Azure ARM APIs rather than direct Resource Provider access.

## CX DNS Zone

The CX zone hosts all customer-facing DNS records for HCPs. These records are created during the HCP provisioning process.

### HCP DNS Records

Each provisioned HCP has four A records in the regional CX zone, all pointing to the shared ingress of the management cluster.

- `api.{hcpname}.{unique-slug}.{regionalCxZone}` - OpenShift API server
- `ignition-server.{hcpname}.{unique-slug}.{regionalCxZone}` - Ignition configuration server
- `konnectivity-server.{hcpname}.{unique-slug}.{regionalCxZone}` - Cluster connectivity service
- `oauth.{hcpname}.{unique-slug}.{regionalCxZone}` - OAuth authentication server

Example: `api.my-hcp.0ulz.uksouth.aroapp-hcp.io`

### Delegated Subzones

For DNS records that point to customer infrastructure, a DNS subzone is created in the cluster's managed resource group and sets up delegation from the regional CX zone.

- **NS Record**: `aro.{hcpname}.{unique-slug}.{regionalCxZone}`
- **Delegates to**: DNS zone in the cluster's managed resource group

This zone contains a wildcard CNAME record for HCPs ingress controller: `*.apps.aro.{hcpname}.{unique-slug}.{regionalCxZone}`

### DNS Record Lifecycle

All CX DNS records are managed by CS during HCP provisioning. The CS managed identity has permissions to manage records only within its regional CX zone, ensuring proper isolation between regions. CS uses the FPA identity to create and manage the delegated DNS subzone in the cluster's managed resource group.

None of these records are changed after HCP provisioning.

## Environment-Specific Configuration

- **Red Hat environments**: `config/config.yaml` under `clouds.dev.defaults.dns`
- **Microsoft environments**: `sdp-pipelines` repository in Azure DevOps under `hcp/config.msft.sensitive.clouds-overlay.yaml` under `clouds.public`
