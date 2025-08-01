### SWIFT V2 Setup

Basic instructions to get swift working in our dev environment. For this you will need access to our mock 1P app. The `login_fpa.sh` script will collect the certificate and log you in as the 1P app, its used to create/delete the service assocciation link on the subnet created by the `00_create_sal_infra.sh`.

The sal_env_vars script stores some basic configuration that is specific to the dev environment (subscription). This would need to be modified for other environments such as INT

You need to create a cluster in dev with the correct vnet tag, instance types and agent pool flags. This can be done with the following; 

`DEPLOY_ENV=swft make infra.all`

1. Create infrastructure, resource group, vnet and subnet. The script as part of the subnet creation will delegate the subnet to the Microsoft.RedHatOpenShift/hcpOpenShiftClusters service

`./00_create_sal_infra.sh`

2. Validate that we can create the service association link

`./01_validate_sal.sh`

3. Create the service association link on the subnet

`./02_create_sal.sh`

4. Create the `PodNetwork` CR

`./03_create_podnetwork.sh`

Only the 1P app can delete the subnet delegation, if the user tries to delete the subnet or resource group that it resides in it will fail. 

`05_delete_sal.sh`