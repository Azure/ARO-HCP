# ARO HCP Service Components

## RP Frontend

- **Purpose**: The RP frontend is the primary regional API entry point for the ARO HCP service.
- **Main Responsibilities**:
  - Translates ARM API requests to Cluster Service API requests
  - Authentication and authorization with the help of MISE
- **Ingress**: Istio ingress gateway via AKS ingress public IP

* It keeps track of cluster state in CosmosDB
* It delegates authentication to [MISE](../frontend/deploy/charts/mise) through an Istio [AuthorizationPolicy](../frontend/deploy/templates/ext-authz.authorizationpolicy.yaml)
* It delegates the HCP creation to Clusters Service
* Communication with Clusters Service is done via the Istio with mTLS and policies in place

## RP Backend

* Updates the state of the hosted control plane in CosmosDB to support async operations

## Clusters Service

* orchestrates HCP creation by
  * provisioning Azure cloud resources within the customer tenant
    * Loadbalancer
    * DNS zone
  * ... and ARO HCP service tenants
    * DNS records and delegations to the customer DNS zone
  * placing Hypershift related manifests onto the managemenet clusters (via Maestro) to drive the actual control plane creation
* provide an opinionated API for day 2 operations like
  * node pool management
  * upgrade management
  * breakglass credential management
* Clusters Service communicates with the Maestro Server via GRPC over Istio
* Clusters Service is not exposed outside of the service cluster

## Maestro Server

* In a nutshell, Maestro is a `kubectl apply over MQTT` service to bridge the gap between Clusters Service and the management cluster
* The Maestro Server is transfering `ManifestWork` resources from Clusters Service to the management cluster
* ... and transfers status back for Clusters Service to read (e.g. `HostedCluster` status)
* The Maestro Server runs on the service cluster in the `maestro` namespace
* The Maestro Server is not exposed outside of the service cluster
* ... because only Clusters Service needs to access it
* It uses a certificate from the mgmt KV to authenticate with the Eventgrid Namespace MQTT broker
* ... and consums it via CSI secret store


## Maestro Agent

* The Maestro Agent is the client component of Maestro running on each management cluster
* ... receiving `ManifestWork` resources from the Maestro Server via MQTT (Azure Eventgrid Namespaces)
* ... applying them to the management cluster
* ... and sending status updates back to the Maestro Server
* The Maestro Agent runs on the management cluster in the `maestro` namespace
* It uses a certificate from the mgmt KV to authenticate with the Eventgrid Namespace MQTT broker
* ... and consums it via CSI secret store


## Hypershift

* Hypershift is an operator for hosted controlplanes running on the management cluster in the `hypershift` namespace
* `HostedCluster` CRs are create by Clusters Service in `ocm-xxx-${CLUSTER_ID}` namespaces (via Maestro)
* Hypershift reacts to `HostedCluster` CRs and provisiones control planes for each of them
* ... by creating an `ocm-xxx-${CLUSTER_ID}-${CLUSTER_NAME}` namespace to host the control plane pods, secrets, etc.
