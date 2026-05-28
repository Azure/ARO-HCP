# Pipeline Documentation

The tree of pipelines making up the ARO HCP service are documented here from the topology configuration.
[ADO Pipeline Overview](https://dev.azure.com/msazure/AzureRedHatOpenShift/_build?definitionScope=%5COneBranch%5Csdp-pipelines%5Chcp)

- Microsoft.Azure.ARO.HCP.Global ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/global-pipeline.yaml)): Deploy global shared infrastructure. (Global)
  - Microsoft.Azure.ARO.HCP.Geography ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/geography-pipeline.yaml)): Deploy geography-level shared infrastructure.
    - Microsoft.Azure.ARO.HCP.Region ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/region-pipeline.yaml)): Deploy regional shared infrastructure. (Region)
      - Microsoft.Azure.ARO.HCP.Service.Infra ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/svc-pipeline.yaml)): Deploy the service cluster and supporting infrastructure.
        - Microsoft.Azure.ARO.HCP.Maestro.Server ([ref](https://github.com/Azure/ARO-HCP/tree/main/maestro/server/pipeline.yaml)): Deploy the Maestro Server.
        - Microsoft.Azure.ARO.HCP.ClusterService ([ref](https://github.com/Azure/ARO-HCP/tree/main/cluster-service/pipeline.yaml)): Deploy Cluster Service.
        - Microsoft.Azure.ARO.HCP.RP.Backend ([ref](https://github.com/Azure/ARO-HCP/tree/main/backend/pipeline.yaml)): Deploy the RP Backend.
        - Microsoft.Azure.ARO.HCP.RP.Frontend ([ref](https://github.com/Azure/ARO-HCP/tree/main/frontend/pipeline.yaml)): Deploy the RP Frontend.
        - Microsoft.Azure.ARO.HCP.SessionGate ([ref](https://github.com/Azure/ARO-HCP/tree/main/sessiongate/pipeline.yaml)): Deploy the Session Gate.
        - Microsoft.Azure.ARO.HCP.AdminAPI ([ref](https://github.com/Azure/ARO-HCP/tree/main/admin/pipeline.yaml)): Deploy the Admin API.
        - Microsoft.Azure.ARO.HCP.Fleet ([ref](https://github.com/Azure/ARO-HCP/tree/main/fleet/pipeline.yaml)): Deploy the Fleet controller.
      - Microsoft.Azure.ARO.HCP.Management.Infra ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/mgmt-pipeline.yaml)): Deploy a management cluster and backing infrastructure.
        - Microsoft.Azure.ARO.HCP.Velero ([ref](https://github.com/Azure/ARO-HCP/tree/main/velero/pipeline.yaml)): Deploy Velero for HostedCluster backup and restore.
        - Microsoft.Azure.ARO.HCP.SecretSyncController ([ref](https://github.com/Azure/ARO-HCP/tree/main/secret-sync-controller/pipeline.yaml)): Deploy the Secret Sync Controller.
        - Microsoft.Azure.ARO.HCP.ACM ([ref](https://github.com/Azure/ARO-HCP/tree/main/acm/pipeline.yaml)): Deploy Advanced Cluster Management and Multi-Cluster Engine.
        - Microsoft.Azure.ARO.HCP.RP.HypershiftOperator ([ref](https://github.com/Azure/ARO-HCP/tree/main/hypershiftoperator/pipeline.yaml)): Deploy the HyperShift operator.
        - Microsoft.Azure.ARO.HCP.Maestro.Agent ([ref](https://github.com/Azure/ARO-HCP/tree/main/maestro/agent/pipeline.yaml)): Deploy the Maestro Agent and register it with the MQTT stream.
        - Microsoft.Azure.ARO.HCP.KubeApplier ([ref](https://github.com/Azure/ARO-HCP/tree/main/kube-applier/pipeline.yaml)): Deploy the Kube Applier.
        - Microsoft.Azure.ARO.HCP.RouteMonitorOperator ([ref](https://github.com/Azure/ARO-HCP/tree/main/route-monitor-operator/pipeline.yaml)): Deploy the Route Monitor Operator.
        - Microsoft.Azure.ARO.HCP.MgmtAgent ([ref](https://github.com/Azure/ARO-HCP/tree/main/mgmt-agent/pipeline.yaml)): Deploy the Management Agent.
      - Microsoft.Azure.ARO.HCP.Monitoring ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/monitoring-pipeline.yaml)): Deploy the Monitoring resources
      - Microsoft.Azure.ARO.HCP.E2E ([ref](https://github.com/Azure/ARO-HCP/tree/main/test/e2e-pipeline.yaml)): Run the E2E tests towards a region and gate SDP progression.
- Microsoft.Azure.ARO.HCP.Management.Delete ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/cleanup/delete.mgmt.pipeline.yaml)): Delete the management resources and management resource group
- Microsoft.Azure.ARO.HCP.Service.Delete ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/cleanup/delete.svc.pipeline.yaml)): Delete the service resources and service resource group
- Microsoft.Azure.ARO.HCP.Region.Delete ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/cleanup/delete.region.pipeline.yaml)): Delete the region resources and resource group
- Microsoft.Azure.ARO.HCP.Observability ([ref](https://github.com/Azure/ARO-HCP/tree/main/observability/tracing/pipeline.yaml)): Deploy the development tracing stack.
- Microsoft.Azure.ARO.HCP.Kusto.Delete ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/cleanup/delete.kusto.instance.pipeline.yaml)): Delete the kusto instance.
- Microsoft.Azure.ARO.HCP.Service.Kubeconfig ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/svc-kubeconfig.yaml)): Grant access to AKS SVC AKS Clusters, mainly intended for E2E test setup.
- Microsoft.Azure.ARO.HCP.Management.Kubeconfig ([ref](https://github.com/Azure/ARO-HCP/tree/main/dev-infrastructure/mgmt-kubeconfig.yaml)): Grant access to AKS SVC AKS Clusters, mainly intended for E2E test setup.
