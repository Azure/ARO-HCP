# Clusters Service PR Check environment

The CS PR check environment is used as a sandbox environment to test changes to Clusters Service in the context of PR checks.

## Differences to the Integrated DEV environment

TBD

## CI/CD

All changes to the infrastructure definitions, configuration and service manifests are PR checked towards the CS PR environment. The driver for such deployments is the [ARO HCP Continous Deployment GitHub Action Workflow](../.github/workflows/aro-hcp-cd.yml). On PR merge, this Workflow will exeecute the immediate deployment of these changes to the CS PR environment.

> [!NOTE]
> Since the this environment is the target for PR checks with infrastructure [bicep what-if](bicep.md#dry-runs) and [service deployment dry-runs](service-deployment-concept.md#deployment-via-pipelines), an unhealthy environment will potentially lead to partial failures of PR checks.

## Access the CS PR environment

The infrastructure resources for this environment can be found in the Azure portal under the following resource groups:

- **hcp-underlay-cspr**: holds the regional resources like Eventgrid, DNS zones, ...
- **hcp-underlay-cspr-svc**: holds the service cluster and supporting infra for its components
- **hcp-underlay-cspr-mgmt-1**: holds the management cluster and supporting infra for its components
- **global**: holds some resources shared by ALL environments in the Red Hat Azure tenant, e.g. the shared ACRs `arohcpsvcdev` and `arohcpocpdev`

To access the service and management cluster of CS PR, make sure you have an active Azure session with your Red Hat account and run these respective commands:

  ```sh
  # service cluster
  DEPLOY_ENV=cs-pr make infra.svc.aks.kubeconfig
  export KUBECONFIG=$(DEPLOY_ENV=cs-pr make infra.svc.aks.kubeconfigfile)

  # management cluster
  DEPLOY_ENV=cs-pr make infra.mgmt.aks.kubeconfig
  export KUBECONFIG=$(DEPLOY_ENV=cs-pr make infra.mgmt.aks.kubeconfigfile)
  ```
