### SWIFT V2 Setup

> [!NOTE]
> All `pers` environments are now Swift enabled by default. Use the e2e tests in `test/` for creating Swift clusters. These docs remain as a reference for any changes needed based on installation.

Basic instructions to get swift working in our dev environment. For this you will need access to our mock 1P app. The `login_fpa.sh` script will collect the certificate and log you in as the 1P app, it's used to create/delete the service association link on the customer subnet.

The `swift_env_vars` script stores some basic configuration that is specific to the dev environment (subscription). This would need to be modified for other environments such as INT

NOTE: Yes when you start creating the SAL, PodNetwork etc you will get logged in and out of Azure because we're switching between the mock 1p and the dev sub

All personal dev environments (`DEPLOY_ENV=pers`) have Swift V2 enabled by default.

`make personal-dev-env`

Refer to `demo/README.md` for instructions on creating infrastructure and clusters via ARM.

1. Validate that we can create the service association link (this should return a 200)

`./01_validate_sal.sh`

2. Create the service association link on the subnet

`./02_create_sal.sh`

> [!IMPORTANT]
> These should be executed on the management cluster not the service cluster

3. Create the `PodNetwork` CR

`./03_create_podnetwork.sh`

4. Create the `PodNetworkInstance` CR

`./04_create_podnetworkinstance.sh <ocm namespace>`

5. Create labels on kube-apiserver pods (this is an example not the solution)

`./05_create_labels_kube_api.sh <ocm namespace>`

6. Validate multitenantpodnetworkconfig CR which should show status with interface details

`./06_validate_mtpnc.sh <ocm namespace>`

Note: You cannot delete the mtpnc or pni until all IPs have been released. This happens when the linked pods (kube-api server in this example) are deleted. This section needs to be extended.

Only the 1P app can delete the subnet delegation, if the user tries to delete the subnet or resource group that it resides in it will fail.

`07_delete_sal.sh`