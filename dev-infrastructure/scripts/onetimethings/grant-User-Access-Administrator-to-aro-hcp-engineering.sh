# aro-hcp-engineering 
# https://portal.azure.com/#view/Microsoft_AAD_IAM/GroupDetailsMenuBlade/~/Overview/groupId/366b619c-e72e-4278-8aaf-9af7851c601f/menuId/

HCPDEVSUBSCRIPTION="ARO Hosted Control Planes (EA Subscription 1)"
HCPDEVSUBSCRIPTIONID="1d3378d3-5a3f-4712-85a1-2485495dfc4b"
AROHCPENGINEERSGROUPID="366b619c-e72e-4278-8aaf-9af7851c601f" 

az role assignment create \
    --assignee $AROHCPENGINEERSGROUPID \
    --scope /subscriptions/$HCPDEVSUBSCRIPTIONID \
    --role "User Access Administrator" 