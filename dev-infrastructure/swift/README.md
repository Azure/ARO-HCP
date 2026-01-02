### SWIFT V2 Setup

Basic instructions to get swift working in our dev environment. For this you will need access to our mock 1P app. The `login_fpa.sh` script will collect the certificate and log you in as the 1P app, its used to create/delete the service assocciation link on the subnet created by the `demo/02-customer-infra.sh`.

The sal_env_vars script stores some basic configuration that is specific to the dev environment (subscription). This would need to be modified for other environments such as INT

NOTE: Yes when you start creating the SAL, PotNetwork etc you will get logged in and out of Azure becuase we're switching between the mock 1p and the dev sub

You need to create a cluster in dev with the correct vnet tag, instance types and agent pool flags. This can be done with the following;

`DEPLOY_ENV=swft make entrypoint/Region`

1. Port forward to the aro-hcp frontend

`export KUBECONFIG=$(make infra.svc.aks.kubeconfigfile)`

`kubectl port-forward svc/aro-hcp-frontend 8443:8443 -n aro-hcp`

2. Create infrastructure, resource group, vnet and subnet. Pass the "swift" argument to the demo script so it will create a second subnet for Swift and delegate it to the Microsoft.RedHatOpenShift/hcpOpenShiftClusters service

`./demo/01-register-sub.sh`

`./demo/02-customer-infra.sh swift`

3. Create cluster and node pool

`./demo/03-create-cluster.sh`

`./demo/04-create-nodepool.sh`

2. Validate that we can create the service association link (this should return a 200)

`./01_validate_sal.sh`

3. Create the service association link on the subnet

`./02_create_sal.sh`

> [!IMPORTANT]
> These should be executed on the management cluster not the service cluster

4. Create the `PodNetwork` CR

`./03_create_podnetwork.sh`

5. Create the `PodNetworkInstance` CR

`./04_create_podnetworkinstance.sh <ocm namespace>`

6. Create labels on kube-apiserver pods (this is an example not the solution)

`./05_create_labels_kube_api.sh <ocm namespace>`

7. Validate multitenantpodnetworkconfig CR which should show status with interface details

`./06_validate_mtpnc.sh <ocm namespace>`

Note: You cannot delete the the mtpnc or pni until all IP's have been released. This happens when the linked pods (kube-api server in this example) are deleted. This section needs to be extended.

Only the 1P app can delete the subnet delegation, if the user tries to delete the subnet or resource group that it resides in it will fail.

`07_delete_sal.sh`