# Create an HCP

## Prepare

* have a `KUBECONFIG` for a SC and MC, e.g. for the [integrated DEV environment](../dev-infrastructure/docs/development-setup.md#access-integrated-dev-environment)
* port-forward RP running on SC: `kubectl port-forward -n aro-hcp svc/aro-hcp-frontend 8443:8443`
* (optional but useful) port-forward CS running on SC: `kubectl port-forward -n cluster-service svc/clusters-service 8001:8000`
* (optional but useful) port-forward Maestro running on SC: `kubectl port-forward -n maestro svc/maestro 8002:8000`

## Register the subscription with the RP

The RP needs to know the subscription in order to be able to create cluters in it.
Run the following command `once` to make the subscription known to the RP.

```bash
./01-register-sub.sh
```

## Create VNET and NSG

We provision HCPs with BYO VNET, so we need to create the VNET, Subnet and NSG upfront.

```bash
./02-customer-infra.sh
```

The resources are created in a resourcegroup named `$USER-net-rg`.

## Create cluster

Create an HCP by sending a request to the port-forwarded RP. You can find a payload template in `cluster.tmpl.json`.
To fill that template with some user specific defaults and send it to the RP, run the following command:

```bash
./03-create-cluster.sh
```

This creates an HCP named `$USER`. If you want to use a different name, run

```bash
CLUSTER_NAME=abc ./03-create-cluster.sh
```

Observe the cluster creation with `./query-cluster-rp.sh` until `properties.provisioningState` is (hopefully) `Succeeded`.
`properties.api.url` holds the URL to the API server of the HCP.

See [Get the kubeconfig for an HCP](#get-the-kubeconfig-for-an-hcp) on how to get the kubeconfig for the HCP.

## Create nodepool

```bash
./04-create-nodepool.sh
```

This creates a nodepool named `np-1` for the previously created cluster.

To check progress on

* the RP, run `./query-nodepool-rp.sh`
* the MC, run `kubectl get nodepool -A` and `kubectl get azuremachine -A`
* within the HCP, run `kubectl get nodes` while using your HCP kubeconfig

## Delete cluster

```bash
./05-delete-cluster.sh
```

## Observe and debug

### Check RP pod logs

```bash
kubectl logs deployment/aro-hcp-frontend -c aro-hcp-frontend -n aro-hcp -f
kubectl logs deployment/aro-hcp-backend -c aro-hcp-backend -n aro-hcp -f
```

### Check cluster state in RP

```bash
./query-cluster-rp.sh
```

### Check CS pod logs

```bash
kubectl logs deployment/clusters-service -c service -f
```

### Check cluster state in CS

```bash
curl localhost:8001/api/clusters_mgmt/v1/clusters | jq
```

### Check Maestro logs

```bash
kubectl logs deployment/maestro -n maestro -c service -f
```

### Check MC consumer registration in Maestro

```bash
curl localhost:8002/api/maestro/v1/consumers | jq
```

### Check MC resource bundles in Maestro

```bash
curl localhost:8002/api/maestro/v1/resource-bundles | jq
```

### Check for manifestwork on MC

```bash
CS_CLUSTER_ID=$(curl localhost:8001/api/clusters_mgmt/v1/clusters | jq .items[0].id -r)
kubectl get manifestwork -n local-cluster | grep ^${CS_CLUSTER_ID}
```

### Check for `HostedCluster` CR on MC

```bash
kubectl get hostedcluster -A
```

### Check for namespaces on MC

```bash
kubectl get ns | grep "ocm.*${CS_CLUSTER_ID}"
```

### Two namespaces should show up

* `ocm-xxx-${CS_CLUSTER_ID}` - contains Hypershift CRs and secrets for an HCP
* `ocm-xxx-${CS_CLUSTER_ID}-${CLUSTER_NAME}` - contains the hosted controlplane (e.g. pods, secrets, ...)

### Get the kubeconfig for an HCP

```bash
kubectl get secret -n ocm-arohcpdev-${CS_CLUSTER_ID} ${CLUSTER_NAME}-admin-kubeconfig -o json | jq .data.kubeconfig -r | base64 -d > my.kubeconfig
```

### Check nodepool on RP

```bash
./query-nodepool-rp.sh
```
