# Pipeline Documentation
The tree of pipelines making up the ARO HCP service are documented here from the topology configuration.

- Microsoft.Azure.ARO.HCP.Global ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/global-pipeline.yaml)): Deploy global shared infrastructure. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=378908)] (Global)
  - Microsoft.Azure.ARO.HCP.Region ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/region-pipeline.yaml)): Deploy regional shared infrastructure. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=381618)] (Region)
    - Microsoft.Azure.ARO.HCP.Service.Infra ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/svc-pipeline.yaml)): Deploy the service cluster and supporting infrastructure. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=367765)] (Service Cluster)
      - Microsoft.Azure.ARO.HCP.ACRPull ([ref](https://github.com/Azure/ARO-HCP/tree/main/acrpull/pipeline.yaml)): Manage ACR pull credentials.
      - Microsoft.Azure.ARO.HCP.Maestro.Server ([ref](https://github.com/Azure/ARO-HCP/tree/main/maestro/server/pipeline.yaml)): Deploy the Maestro Server. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=382258)]
      - Microsoft.Azure.ARO.HCP.ClusterService ([ref](https://github.com/Azure/ARO-HCP/tree/main/cluster-service/pipeline.yaml)): Deploy Cluster Service. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=402943)]
      - Microsoft.Azure.ARO.HCP.RP.Backend ([ref](https://github.com/Azure/ARO-HCP/tree/main/backend/pipeline.yaml)): Deploy the RP Backend. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=398456)]
      - Microsoft.Azure.ARO.HCP.RP.Frontend ([ref](https://github.com/Azure/ARO-HCP/tree/main/frontend/pipeline.yaml)): Deploy the RP Frontend. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=398460)]
    - Microsoft.Azure.ARO.HCP.Management.Infra ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/mgmt-pipeline.yaml)): Deploy a management cluster and backing infrastructure. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=372877)] (Management Cluster)
      - Microsoft.Azure.ARO.HCP.Management.Infra.Solo ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/mgmt-solo-pipeline.yaml)): 
      - Microsoft.Azure.ARO.HCP.ACM ([ref](https://github.com/Azure/ARO-HCP/tree/main/acm/pipeline.yaml)): Deploy Advanced Cluster Management and Multi-Cluster Engine. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=402939)]
      - Microsoft.Azure.ARO.HCP.RP.HypershiftOperator ([ref](https://github.com/Azure/ARO-HCP/tree/main/hypershiftoperator/pipeline.yaml)): Deploy the HyperShift operator. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=402934)]
      - Microsoft.Azure.ARO.HCP.RP.Istio ([ref](https://github.com/Azure/ARO-HCP/tree/main/istio/pipeline.yaml)): Deploy Istio.
      - Microsoft.Azure.ARO.HCP.PKO ([ref](https://github.com/Azure/ARO-HCP/tree/main/pko/pipeline.yaml)): Deploy the Package Operator.
      - Microsoft.Azure.ARO.HCP.ACRPull ([ref](https://github.com/Azure/ARO-HCP/tree/main/acrpull/pipeline.yaml)): Manage ACR pull credentials.
      - Microsoft.Azure.ARO.HCP.Maestro.Agent ([ref](https://github.com/Azure/ARO-HCP/tree/main/maestro/agent/pipeline.yaml)): Deploy the Maestro Agent and register it with the MQTT stream. [[INT](https://msazure.visualstudio.com/AzureRedHatOpenShift/_build?definitionId=388100)]
