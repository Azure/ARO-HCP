# How to run CS in DemoMode on AKS

## 0. Demo Mode

Demo Mode in CS means "no external dependencies required for runtime". This allows CS to get deployed
quickly to any *KS service, but a further TODO is required to re-implement Demo Mode and ARO-HCP Mode using 
the upcoming service locator pattern.

## 1. AKS cluster

Have an AKS cluster provisioned with KUBECONFIG ready.

For provisioning AKS with an Azure Container Registry (ACR), see: TODO

## 2. Authenticate with ACR

The Makefile contains `make aks/registry`, which authenticates your local `podman` with your cluster's ACR:

`az acr login --name $(acr_name) --expose-token | jq -r .accessToken | podman login  $(external_image_registry) --username=00000000-0000-0000-0000-000000000000 --password-stdin`

## 3. Build and push

The Makefile contains `make aks/deploy`, which builds a CS image, pushes it to the ACR, and deploys the necessary secrets/services/deployments.

When the deployment is successful, you'll see pods like this:

```shell
$ oc get pods
NAME                           READY   STATUS    RESTARTS   AGE
clusters-service-9b87d-dxwns   1/1     Running   0          2m39s
ocm-cs-db-547968bf5c-rt8q2     1/1     Running   0          2m41s
```

We'll be using both of those pod names in the next steps. First we'll connect to the database and then we'll post
to the API server.


## 4. Connect to your database

Using your database pod name from above, get a bash shell in the running pod:

```shell
kubectl exec -i -t ocm-cs-db-547968bf5c-rt8q2 -- /bin/bash 
```

Your database is running with environment variables. Use them to login like this:

```shell
PGPASSWORD=$POSTGRES_PASSWORD psql -U $POSTGRES_USER $POSTGRES_DB
```

```shell
$ kubectl exec -i -t ocm-cs-db-547968bf5c-rt8q2 -- /bin/bash 
root@ocm-cs-db-547968bf5c-rt8q2:/# PGPASSWORD=$POSTGRES_PASSWORD psql -U $POSTGRES_USER $POSTGRES_DB
psql (16.2 (Debian 16.2-1.pgdg120+2))
Type "help" for help.

ocm-cs-db=# \dt
                       List of relations
 Schema |                 Name                  | Type  | Owner 
--------+---------------------------------------+-------+-------
 public | addon_additional_catalog_sources      | table | ocm
 public | azure_node_pools                      | table | ocm
 public | azure_settings                        | table | ocm
 ..
 public | version_gate_agreements               | table | ocm
 public | version_gates                         | table | ocm
 public | versions                              | table | ocm
 public | wif_configs                           | table | ocm
 public | wif_templates                         | table | ocm
(84 rows)

```

## 4. Auth w/ API

ARO HCP is a 1st Party service in Azure, so any API calls to CS are authenticated and authorized. 
As such, CS in ARO HCP does not use RH SSO. But development mode is deployed 3rd party and traffic over the 
internet can't be trusted. How do we access the running API without opening up the service publicly?

`kubectl port-forward` solves this problem for us by running a localhost service on a specific port and forwards it
to the pod and port of your choice.

Using the pod name obtained in the previous step, we can set up port forwarding like this:

```shell
$ kubectl port-forward clusters-service-9b87d-dxwns 8000:8000
Forwarding from 127.0.0.1:8000 -> 8000
Forwarding from [::1]:8000 -> 8000
```

## 5. Use the API

Simple `curl` commands without any RH token work fine:

```shell

$ curl http://localhost:8000/api/clusters_mgmt/v1/clusters | jq
  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current
                                 Dload  Upload   Total   Spent    Left  Speed
100    61  100    61    0     0    649      0 --:--:-- --:--:-- --:--:--   655
{
  "kind": "ClusterList",
  "page": 0,
  "size": 0,
  "total": 0,
  "items": []
}

```

And if you normally use your `ocm` client for local development, that works perfectly with the `kubectl port-foward` on `localhost:8000`.

 > Note: The OCM  token used below isn't used at runtime when making queries, but it's required to set the ocm client correctly.

```shell

$ ocm login --token=${OCM_ACCESS_TOKEN} --url=http://localhost:8000
 
$ ocm list clusters
ID                                NAME                                                    API URL                                                     OPENSHIFT_VERSION   PRODUCT ID  HCP      CLOUD_PROVIDER  REGION ID       STATE        


$ ocm post /api/clusters_mgmt/v1/clusters  << EOF
{
    "name": "yourfakecluster",
    "region": {
      "id": "eu-west-1"
    },
    "properties": {
      "fake_cluster": "true"
    },
    "managed": false,
    "product" :{
      "id": "osd"
    },
    "cloud_provider": {
      "id": "aws"
    },
    "storage_quota": {
      "value": 107374182400
    },
    "load_balancer_quota": 4
}
EOF

ocm post /api/clusters_mgmt/v1/clusters << EOF
{
  "name": "hcp-1",
  "product": {
    "id": "aro"
  },
  "ccs": {
    "enabled": true
  },
  "region": {
    "id": "westus3"
  },
  "hypershift": {
    "enabled": true
  },
  "multi_az": true,
  "azure": {
    "subscription_id": "ms_test_subs_id_1",
    "resource_group_name": "ms_rg_name_1",
    "resource_name": "ms_resource_name_1",
    "tenant_id": "ms_tenant_id_1",
    "managed_resource_group_name": "ms_mgr_name_1",
    "subnet_resource_id": "/subscriptions/ms_test_subs_id_1/resourcegroups/customrg1/providers/Microsoft.Network/virtualNetworks/myvnet/subnets/mysubnet",
    "network_security_group_resource_id": "/subscriptions/ms_test_subs_id_1/resourceGroups/aro-infra-lvccho2q-adodemo/providers/Microsoft.Network/networkSecurityGroups/adodemo-hwgnq-nsg"

  },
  "properties": {
    "provision_shard_id": "azdemo"
  }
}
EOF


$ ocm list clusters
ID                                NAME                          API URL                                                     OPENSHIFT_VERSION   PRODUCT ID  HCP      CLOUD_PROVIDER  REGION ID       STATE                
2b04fg1kjki5uvc8smbr3phgeq5ikpjq  yourfakecluster                 NONE                                                        NONE                osd         false    aws             eu-west-1       error

```        
