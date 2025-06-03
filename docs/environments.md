# ARO HCP Tenants and Environments

## Azure Tenants

### Red Hat Tenant (Development Tenant)

- Access: Requires a Red Hat (RH) account
- Purpose: Hosts various development and test-related environments
- Pros:
  - full controll over the environment
  - small entry barrier
  - nearly no restrictions
  - quick development cycles
- Cons:
  - nearly no restrictions
  - no access to EV2 deployments
  - no access to special Azure features (OneCert, SWIFT, MSI-RP, ...)
  - no MISE support

### MSIT Corp Tenant (MSFT INT Tenant)

- Access: Requires a **b-** account, accessed via Windows Virtual Desktop (WVD) or Virtual Machine (VM)
- Purpose: Hosts the ARO HCP INT environment, testing EV2 rollouts and ARM interaction
- Pros:
  - access to EV2 deployments
  - self-service EV2 deployments to retain a certain level of control and speed
  - access to certain special Azure features (OneCert, Kusto, ...)
  - MISE support
  - getting feedback of MSFT compliance requirements
- Cons:
  - higher entry barrier with b- account
  - no access to certain Azure features (SWIFT, MSI-RP, ...)
  - restrictive managed identity federation...
  - ... combined with no multi-tenant app support complicates testing

### AME Tenant (Azure Production Tenant)

- Access: Requires an **AME** account, accessed from a SAW device
- Purpose: Hosts the ARO HCP **Stage** and **Production** environments
- Pros:
  - real life EV2 production deployments
  - access to all required Azure features
  - MISE support
- Cons:
  - highest entry barrier with AME account and SAW devices
  - slow deployment cycles

## ARO HCP Environment Overview

This table provides an overview of the various ARO HCP deployment environments, their Azure tenants and available capabilities and features the environment supports.

| Deployment Environment                                                      | Service Azure Tenant | User Azure Tenant | Multi-tenant support | OneCert | Service Mock SPs | ARM integration | MSI RP | MSFT Compliance required | MISE | Deployment Driver | Logs                | Config / Env                                          |
| :-------------------------------------------------------------------------- | :------------------- | :---------------- | :------------------- | :------ | :--------------- | :-------------- | :----- | :----------------------- | :--- | :---------------- | :------------------ | :---------------------------------------------------- |
| [Personal DEV Env](personal-dev.md)                                         | Red Hat / Public     | Red Hat / Public  | No                   | No      | Yes              | No              | No     | No                       | No   | Makefile          | in-cluster Pod logs | [config.yaml](../config/config.yaml) / pers           |
| [Developer Machine](personal-dev.md#partial-personal-dev-environment-setup) | Red Hat / Public     | Red Hat / Public  | No                   | No      | Yes              | No              | No     | No                       | No   | Makefile          | local               | [config.yaml](../config/config.yaml) / pers *         |
| Integrated / Shared Dev                                                     | Red Hat / Public     | Red Hat / Public  | No                   | No      | Yes              | No              | No     | No                       | No   | GH Actions        | Azure Log Analytics | [config.yaml](../config/config.yaml) / dev            |
| CS PR                                                                       | Red Hat / Public     | Red Hat / Public  | No                   | No      | Yes              | No              | No     | No                       | No   | GH Actions        | Azure Log Analytics | [config.yaml](../config/config.yaml) / cspr           |
| Integration                                                                 | MSIT Corp / Public   | Red Hat / Public  | No                   | Yes     | Yes              | Yes             | No     | Yes                      | Yes  | EV2               | Geneva              | [config.msft.yaml](../config/config.msft.yaml) / int  |
| Staging                                                                     | AME / Public         | Undefined         | Yes                  | Yes     | No               | Yes             | Yes    | Yes                      | Yes  | EV2               | Geneva              | [config.msft.yaml](../config/config.msft.yaml) / -    |
| Production                                                                  | AME / Public         | Undefined         | Yes                  | Yes     | No               | Yes             | Yes    | Yes                      | Yes  | EV2               | Geneva              | [config.msft.yaml](../config/config.msft.yaml) / -    |

- **Deployment Environment**: This is the name of an ARO HCP deployment environment as the team refers to it. By using this name, the team understands the context and capabilities of the environment.
- **Service Azure Tenant**: The Azure tenant where the ARO HCP service is deployed, e.g. where the Service and Management Clusters are running.
- **User Azure Tenant**: The Azure tenant where the users are creating their clusters and where their ARO HCP dataplane will be running.
- **Multi-tenant support**: Indicates if the environment supports multiple tenants Apps/Service Principals or not. Multi-Tenancy is required if the Service and User Azure Tenant are different.
- **OneCert**: Indicates if the environment supports the OneCert certificate signers/CAs, e.g. for creating certificates directly within Key Vault.
- **Service Mock SPs**: Indicates if the environment requires us to use mocks for First Party App (FPA)
- **ARM integration**: Indicates if the environment will be integrated with Azure Resource Manager (ARM). "We can use ARM to talk to the environment."
- **MSI RP**: Indicates if we can use MSI-RP to get ahold of ARO HCP dataplane managed identities backing certificiates.
- **MSFT Compliance required**: Indicates if the environment requires MSFT Compliance.
- **MISE**: Indicates if we can use MISE for RP frontend authn/z. If not available, we can only use `kubectl port-forward` to access the RP frontend service.
- **Deployment Driver**: The tooling used to deploy / update an environment.
- **Logs**: The log storage solution used for the environment.
- **Config / Env**: A reference to the configuration file and environment name used for the environment.
