# ARO HCP Tenants and Environments

## Azure Tenants

The tenants below are where the ARO HCP **service** (SVC and MGMT clusters) runs. They don't decide which environment a customer subscription uses; see [Which environment does my subscription use?](#which-environment-does-my-subscription-use).

### Red Hat Tenant (Development Tenant)

- Access: Requires a Red Hat (RH) account
- Purpose: Hosts various development and test-related environments
- Pros:
  - full control over the environment
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

| Deployment Environment                                                      | Service Azure Tenant / Cloud | Testing Azure Tenant / Cloud / Subscription                                        | Multi-tenant support | OneCert | Service Mock SPs | ARM integration | MSI RP | MSFT Compliance required | MISE | Deployment Driver | Logs                        | Config / Env                                                                                                                                                                                                                                                                       |
| :-------------------------------------------------------------------------- | :--------------------------- | :--------------------------------------------------------------------------------- | :------------------- | :------ | :--------------- | :-------------- | :----- | :----------------------- | :--- | :---------------- | :-------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [Personal DEV Env](personal-dev.md)                                         | Red Hat / Public             | Red Hat / Public / ARO Hosted Control Planes (EA Subscription 1)                   | No                   | No      | Yes              | No              | No     | No                       | No   | Makefile          | in-cluster Pod logs         | [config.yaml](../config/config.yaml) / pers                                                                                                                                                                                                                                        |
| [Developer Machine](personal-dev.md#partial-personal-dev-environment-setup) | Red Hat / Public             | Red Hat / Public / ARO Hosted Control Planes (EA Subscription 1)                   | No                   | No      | Yes              | No              | No     | No                       | No   | Makefile          | local                       | [config.yaml](../config/config.yaml) / pers *                                                                                                                                                                                                                                      |
| Personal Perfscale Env                                                      | Red Hat / Public             | Red Hat / Public / ARO Hosted Control Planes (EA Subscription 1)                   | No                   | No      | Yes              | No              | No     | No                       | No   | Makefile          | Azure Data Explorer (Kusto) | [config.yaml](../config/config.yaml) / perf                                                                                                                                                                                                                                        |
| [On-demand DEV CI](ci/execution.md#pr-validation-in-dev)                    | Red Hat / Public             | Red Hat / Public / [DEV E2E slot subscriptions](../test/e2e-config/e2e-slots.yaml) | No                   | No      | Yes              | No              | No     | No                       | No   | Prow              | Azure Data Explorer (Kusto) | [config.yaml](../config/config.yaml) / ci00, ci01, ci02                                                                                                                                                                                                                            |
| Integrated / Shared Dev (decommissioned)                                    | Red Hat / Public             | Red Hat / Public / ARO Hosted Control Planes (EA Subscription 1)                   | No                   | No      | Yes              | No              | No     | No                       | No   | Prow              | Azure Log Analytics         | [config.yaml](../config/config.yaml) / dev                                                                                                                                                                                                                                         |
| Integration                                                                 | MSIT Corp / Public           | Red Hat / Public / ARO SRE Team - INT (EA Subscription 3)                          | No                   | Yes     | Yes              | Yes             | No     | Yes                      | Yes  | EV2               | Geneva                      | [config.msft.clouds-overlay.yaml](../config/config.msft.clouds-overlay.yaml) + [ADO sdp-pipelines/hcp/config.msft.sensitive.clouds-overlay.yaml](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/config.msft.sensitive.clouds-overlay.yaml) / int  |
| Staging                                                                     | AME / Public                 | Red Hat / Public / ARO HCP E2E Hosted Clusters - Stage - 00                        | Yes                  | Yes     | No               | Yes             | Yes    | Yes                      | Yes  | EV2               | Geneva                      | [config.msft.clouds-overlay.yaml](../config/config.msft.clouds-overlay.yaml) + [ADO sdp-pipelines/hcp/config.msft.sensitive.clouds-overlay.yaml](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/config.msft.sensitive.clouds-overlay.yaml) / stg  |
| Production                                                                  | AME / Public                 | Red Hat / Public / ARO HCP E2E Hosted Clusters - Prod - 00 / Prod - 01             | Yes                  | Yes     | No               | Yes             | Yes    | Yes                      | Yes  | EV2               | Geneva                      | [config.msft.clouds-overlay.yaml](../config/config.msft.clouds-overlay.yaml) + [ADO sdp-pipelines/hcp/config.msft.sensitive.clouds-overlay.yaml](https://dev.azure.com/msazure/AzureRedHatOpenShift/_git/sdp-pipelines?path=/hcp/config.msft.sensitive.clouds-overlay.yaml) / prod |

> [!NOTE]
> The Integrated / Shared Dev environment has been decommissioned. Only shared global resources (ACRs, DNS zones) are still deployed there via the `global-pipeline-postsubmit` Prow job, as these resources are shared by personal dev environments.

> [!NOTE]
> The testing subscriptions listed are the primary E2E subscriptions.
>
> - [e2e-slots.yaml](../test/e2e-config/e2e-slots.yaml) has the full list for each environment.
> - PROD E2E gating in the `eastus2euap` canary region is pinned to **ARO HCP E2E**, a subscription in the separate [Test Test Azure Red Hat OpenShift tenant](sops/test-test-tenant-access.md).
> - The pin is set by `e2e.regionTest.allowedSubscriptions` in [config.msft.clouds-overlay.yaml](../config/config.msft.clouds-overlay.yaml).

- **Deployment Environment**: This is the name of an ARO HCP deployment environment as the team refers to it. By using this name, the team understands the context and capabilities of the environment.
- **Service Azure Tenant**: The Azure tenant where the ARO HCP service is deployed, e.g. where the Service and Management Clusters are running.
- **User Azure Tenant**: The Azure tenant where the users are creating their clusters and where their ARO HCP dataplane will be running.
- **Multi-tenant support**: Indicates if the environment supports multiple tenants Apps/Service Principals or not. Multi-Tenancy is required if the Service and User Azure Tenant are different.
- **OneCert**: Indicates if the environment supports the OneCert certificate signers/CAs, e.g. for creating certificates directly within Key Vault.
- **Service Mock SPs**: Indicates if the environment requires us to use mocks for First Party App (FPA)
- **ARM integration**: Indicates if the environment will be integrated with Azure Resource Manager (ARM). "We can use ARM to talk to the environment."
- **MSI RP**: Indicates if we can use MSI-RP to get ahold of ARO HCP dataplane managed identities backing certificates.
- **MSFT Compliance required**: Indicates if the environment requires MSFT Compliance.
- **MISE**: Indicates if we can use MISE for RP frontend authn/z. If not available, we can only use `kubectl port-forward` to access the RP frontend service.
- **Deployment Driver**: The tooling used to deploy / update an environment.
- **Logs**: The log storage solution used for the environment.
- **Config / Env**: A reference to the configuration file and environment name used for the environment. INT, STG and PROD combine [config.yaml](../config/config.yaml) with the non-sensitive MSFT overlay in this repository and the sensitive overlay in sdp-pipelines.

## Which environment does my subscription use?

The tenant of a customer subscription doesn't tell you its environment: test subscriptions for INT, STG and PROD all live in the Red Hat tenant. ARM routes `Microsoft.RedHatOpenShift/hcpOpenShiftClusters` requests based on the AFEC (preview feature) flags registered on the **customer subscription**. These flags are the `requiredFeatures` of the RP endpoints in the ARM manifest.

| Registered AFEC flag                         | Environment | Regions              |
| :------------------------------------------- | :---------- | :------------------- |
| `Microsoft.RedHatOpenShift/INT-APPROVED`     | Integration | `westus3`            |
| `Microsoft.RedHatOpenShift/STAGING-APPROVED` | Staging     | `uksouth`, `westus3` |
| neither                                      | Production  | all PROD regions     |

Only flags in the `Registered` state count; a `Pending` or `Unregistered` flag is ignored. INT and STG run only in the regions listed.

These flags don't change the environment:

- `HcpPrivatePreview` exposes older preview API versions.
- `InProgress` exposes the EUAP canary regions (`eastus2euap`, `centraluseuap`). Deploying there also needs `Microsoft.Resources/EUAPParticipation`; see [EUAP/Canary Region Access](ci/e2e-subscription-onboarding.md#euapcanary-region-access).
- `ExperimentalReleaseFeatures` makes the RP honor the `aro-hcp.experimental.*` resource tags that E2E tests use.

To check a subscription's flags, either:

- in the Azure portal, open the subscription, select **Preview features**, and filter on `Microsoft.RedHatOpenShift`, or
- run:

  ```bash
  az feature list --namespace Microsoft.RedHatOpenShift --subscription <subscription> -o table
  ```

To register flags for a new E2E subscription, see [INT/STG/PROD E2E Subscription Onboarding](ci/e2e-subscription-onboarding.md#intstgprod-e2e-subscription-onboarding).
