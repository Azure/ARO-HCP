# ARO HCP Pipelines

This document provides an overview of the pipelines used to deploy ARO HCP.

## Pipeline Inventory

| Pipeline                                                                          | Scope                                                        | Purpose                                                         | ADO Pipelines                                                                           |
| :-------------------------------------------------------------------------------- | :----------------------------------------------------------- | :-------------------------------------------------------------- | :-------------------------------------------------------------------------------------- |
| [Microsoft.Azure.ARO.HCP.Global](dev-infrastructure/global-pipeline.yaml)         | [global](high-level-architecture.md)                         | Manage global shared infrastructure for ARO HCP                 | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=378908) |
| [Microsoft.Azure.ARO.HCP.Region](dev-infrastructure/region-pipeline.yaml)         | [region](high-level-architecture.md#regional-scope)          | Manage regional infrastructure shared by SC and MC              | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=381618) |
| [Microsoft.Azure.ARO.HCP.Service.Infra](dev-infrastructure/svc-pipeline.yaml)     | [svc](high-level-architecture.md#service-cluster)            | Manage service cluster and supporting infra for its services    | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=367765) |
| [Microsoft.Azure.ARO.HCP.Management.Infra](dev-infrastructure/mgmt-pipeline.yaml) | [mgmt](high-level-architecture.md#management-clusters)       | Manage management cluster and supporting infra for its services | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=372877) |
| [Microsoft.Azure.ARO.HCP.Maestro.Server](maestro/server/pipeline.yaml)            | [svc](high-level-architecture.md#service-cluster)            | Manage the Maestro Server on the SC                             | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=382258) |
| [Microsoft.Azure.ARO.HCP.ClusterService](cluster-service/pipeline.yaml)           | [svc](high-level-architecture.md#service-cluster)            | Manage Clusters Service on the SC                               | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=402943) |
| [Microsoft.Azure.ARO.HCP.RP.Backend](backend/pipeline.yaml)                       | [svc](high-level-architecture.md#service-cluster)            | Manage RP Backend on the SC                                     | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=398456) |
| [Microsoft.Azure.ARO.HCP.RP.Frontend](frontend/pipeline.yaml)                     | [svc](high-level-architecture.md#service-cluster)            | Manage RP Frontend on the SC                                    | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=398460) |
| [Microsoft.Azure.ARO.HCP.PKO](pko/pipeline.yaml)                                  | [mgmt](high-level-architecture.md#management-clusters)       | Manage Package Operator on the MC                               |                                                                                         |
| [Microsoft.Azure.ARO.HCP.RP.HypershiftOperator](hypershiftoperator/pipeline.yaml) | [mgmt](high-level-architecture.md#management-clusters)       | Manage the Hypershift Operator on the MC                        | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=402934) |
| [Microsoft.Azure.ARO.HCP.ACM](acm/pipeline.yaml)                                  | [mgmt](high-level-architecture.md#management-clusters)       | Manage ACM and MCE on the MC                                    | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=402939) |
| [Microsoft.Azure.ARO.HCP.Maestro.Agent](maestro/agent/pipeline.yaml)              | [mgmt (svc)](high-level-architecture.md#management-clusters) | Manage the Maestro Agent on the MC and registers it with the SC | [INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=388100) |

> [!NOTE]
> This list is not exhaustive. The ones listed are relevant for both RH tenant and MSFT tenant deployments.
> There are more pipelines defined in the [ADO HCP repository](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionScope=%5COneBranch%5Chcp) which revolve around MSFT tenant only deployments.
