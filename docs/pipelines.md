# Pipeline Documentation

The tree of pipelines making up the ARO HCP service are documented here from the topology configuration.
[ADO Pipeline Overview](https://dev.azure.com/msazure/AzureRedHatOpenShift/_build?definitionScope=%5COneBranch%5Csdp-pipelines%5Chcp)

- Microsoft.Azure.ARO.HCP.Global ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/global-pipeline.yaml)): Deploy global shared infrastructure. (Global)
  - Microsoft.Azure.ARO.HCP.Region ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/region-pipeline.yaml)): Deploy regional shared infrastructure. (Region)
    - Microsoft.Azure.ARO.HCP.Service.Infra ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/svc-pipeline.yaml)): Deploy the service cluster and supporting infrastructure. (Service Cluster)
      - Microsoft.Azure.ARO.HCP.Maestro.Server ([ref](https://github.com/Azure/ARO-HCP/tree/main/maestro/server/pipeline.yaml)): Deploy the Maestro Server.
      - Microsoft.Azure.ARO.HCP.ClusterService ([ref](https://github.com/Azure/ARO-HCP/tree/main/cluster-service/pipeline.yaml)): Deploy Cluster Service.
      - Microsoft.Azure.ARO.HCP.RP.Backend ([ref](https://github.com/Azure/ARO-HCP/tree/main/backend/pipeline.yaml)): Deploy the RP Backend.
      - Microsoft.Azure.ARO.HCP.RP.Frontend ([ref](https://github.com/Azure/ARO-HCP/tree/main/frontend/pipeline.yaml)): Deploy the RP Frontend.
    - Microsoft.Azure.ARO.HCP.Management.Infra ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/mgmt-pipeline.yaml)): Deploy a management cluster and backing infrastructure. (Management Cluster)
      - Microsoft.Azure.ARO.HCP.ACM ([ref](https://github.com/Azure/ARO-HCP/tree/main/acm/pipeline.yaml)): Deploy Advanced Cluster Management and Multi-Cluster Engine.
      - Microsoft.Azure.ARO.HCP.RP.HypershiftOperator ([ref](https://github.com/Azure/ARO-HCP/tree/main/hypershiftoperator/pipeline.yaml)): Deploy the HyperShift operator.
      - Microsoft.Azure.ARO.HCP.PKO ([ref](https://github.com/Azure/ARO-HCP/tree/main/pko/pipeline.yaml)): Deploy the Package Operator.
      - Microsoft.Azure.ARO.HCP.Maestro.Agent ([ref](https://github.com/Azure/ARO-HCP/tree/main/maestro/agent/pipeline.yaml)): Deploy the Maestro Agent and register it with the MQTT stream.
