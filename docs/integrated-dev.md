# ARO HCP Integrated DEV environment

The Integrated DEV environment is designed as a centralized environment where all infrastructure and service changes are automatically integrated via CI/CD GitHub Action pipelines. Unlike [personal DEV environments](personal-dev.md), this environment is not owned by a single person or team but is shared collectively by all developers to test the latest changes as a whole.

This environment ensures that all updates and improvements made in different personal DEV environments are consolidated and tested in a unified environment before moving forward in the deployment pipeline.

## CI/CD

All changes to the infrastructure definitions, configuration and service manifests are PR checked towards the integrated DEV environment. The driver for such deployments is the [ARO HCP Continous Deployment GitHub Action Workflow](../.github/workflows/aro-hcp-cd.yml). On PR merge, this Workflow will exeecute the immediate deployment of these changes to the integrated DEV environment.

> [!NOTE]
> Since the this environment is the target for PR checks with infrastructure [bicep what-if](bicep.md#dry-runs) and [service deployment dry-runs](service-deployment-concept.md#deployment-via-pipelines), an unhealthy environment will potentially lead to partial failures of PR checks.

## Access integrated DEV environment

The infrastructure resources for this environment can be found in the Azure portal under the following resource groups:

- **hcp-underlay-dev**: holds the regional resources like Eventgrid, DNS zones, ...
- **hcp-underlay-dev-svc**: holds the service cluster and supporting infra for its components
- **hcp-underlay-dev-mgmt-1**: holds the management cluster and supporting infra for its components
- **global**: holds some resources shared by ALL environments in the Red Hat Azure tenant, e.g. the shared ACRs `arohcpsvcdev` and `arohcpocpdev`

To access the service and management cluster of integrated DEV make, sure you have an active Azure session with your Red Hat account and run these respective commands:

  ```sh
  # service cluster
  DEPLOY_ENV=dev make infra.svc.aks.kubeconfig
  export KUBECONFIG=$(DEPLOY_ENV=dev make infra.svc.aks.kubeconfigfile)

  # management cluster
  DEPLOY_ENV=dev make infra.mgmt.aks.kubeconfig
  export KUBECONFIG=$(DEPLOY_ENV=dev make infra.mgmt.aks.kubeconfigfile)
  ```
