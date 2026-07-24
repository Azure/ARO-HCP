# Anatomy of an End-to-End Test

Azure RedHat OpenShift Hosted Control Planes (ARO HCP) is an Azure service that allows users to provision managed OpenShift (RedHat's Kubernetes distribution) clusters in Azure. A customer provisions an OpenShift cluster through Azure and interacts with it using normal Kubernetes API semantics.

Azure RedHat OpenShift end-to-end tests generally follow this flow:

- ask the Resource Provider to create an ARO HCP cluster by sending a REST request to Azure Resource Manager (ARM)
- ensure the cluster provisions and is healthy
- take some action, like:
  - modify the service control plane (through ARM) to change the cluster
  - modify the service data plane, which is the customer's cluster control plane (through Kubernetes API) to use Kubernetes functionality
  - modify the customer data plane (through the ARM ARO HCP NodePool API or the OpenShift MachinePool API)
- verify the state of the ARM resources, the customer's cluster, or both

## Debugging Failures

When debugging a failure, determine what interaction failed - was it:

- an Azure client talking to the Resource Provider through the ARM API gateway
- a Kubernetes client talking to the Kubernetes API server
- a Kubernetes client talking to the Kubernetes data plane, proxied to the kubelet API through the Kubernetes API

If the former, consider the ARO HCP service architecture to determine which logs are necessary. If the latter, apply normal Kubernetes and OpenShift debugging practices.

## ARO HCP Architecture

ARO HCP regional architecture is split into two classes of Kubernetes clusters:

- the "service cluster" runs the regional servers for handling the service logic
- the "management clusters" are where customer workloads are scheduled

The Resource Provider (RP) is a catch-all term for all the software that comes together to realize the ARM API for customers. Review ./service-components.md for an in-depth view of these components.

### Life of a Request

A request from a client to the Azure control plane takes the following steps:

1. sent from the client to the Azure API gateway (Azure Resource Manager, ARM)
2. proxied to a regional endpoint of the ARO HCP resource provider frontend
   a. in the default case:
      i. customer intent is recorded to the CosmosDB data store
      ii. asynchronously, the ARO HCP resource provider backend transforms stated customer intent into reality, updating the CosmosDB controller statuses as it works
      iii. the backend controllers proxy their intent into a legacy sub-system called clusters-service, which is responsible for doing the work of turning intent into reality
      iv. clusters-service schedules the customer workloads to a particular management cluster, and proxies the customer intent into a `HostedCluster` document, which is a HyperShift API. maestro is used to apply the `HostedCluster` onto the correct management cluster
      v. HyperShift turns the `HostedCluster` intent into a running OpenShift control plane
      vi. maestro brings the updated status back from the management cluster, updating clusters-service, and, in turn, the backend
   b. in some cases, the frontend may call clusters-service directly; the rest of the flow is identical