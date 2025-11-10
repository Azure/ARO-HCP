
# Deploy CS alongside RP to dev-env

## Motivation

In order to test RP [frontend and/or backend] changes along-side CS changes one is required to deploy CS with your local changes made alongside RP with its respective to your service cluster in [dev-env](https://github.com/Azure/ARO-HCP/blob/main/docs/personal-dev.md).

## Step by step guide:

1. Start by building an image from your CS branch by running `make image`.
2. Next push the image to your target repository. I’ve chosen to push to my private repository. In order to do that you first need to login to your quay account.
```
podman login quay.io
username: <...>
password: <..>
```

3. Make the following changes to your Makefile:
    1. Set `external_image_registry` to `[quay.io](http://quay.io)`.
    2. Set namespace to your user's account or organization. Such that the `image_repository` field is the correct target repository.
4. Push the image to your repository:
```
make push
```
5. In case a private repository is used one will need to create a “Robot Account” by going to the “Account Settings” tab on the far right. Than, go to “Robot Accounts”, Click “Create Robot account” and give it a proper name. Then, give your newly created Robot account “Read” permissions for the repository you have recently created.

6. Click on the “Robot Account” and then on the “Kubernetes Secret” pane download the pull secret.
7. We will then use the instructions provided to apply the cs pull secret and appropriate changes in our service cluster:
```
export KUBECONFIG=$(make infra.svc.aks.kubeconfigfile)
kubectl create -f <secret-name> --namespace=clusters-service
```
8. Make sure to use the correct kubeconfig [under ARO-HCP directory], run:
9. Next edit the service account used by clusters-service in your service cluster:
```
kubectl edit serviceaccount clusters-service -n clusters-service
```
10. Add the pull secret recently created to the list of `imagePullSecrets` in the service account:
```
apiVersion: v1
imagePullSecrets:
- name: acr-pull-pull-binding
- name: <---- // your pull secret added here
kind: ServiceAccount
metadata:
  annotations:
    ...
  creationTimestamp: "2025-11-05T05:59:28Z"
  labels:
    app: clusters-service
    app.kubernetes.io/managed-by: Helm
  name: clusters-service
  namespace: clusters-service
```
11. Change the image field in the deployment file for clusters service to point to your recently pushed image:
```
kubectl edit deployment clusters-service -n clusters-service
```
12. Make sure to edit the image reference everywhere [*both in the init container and in the service container*], e.g.:
```
initContainers:
      - command:
        - /usr/local/bin/clusters-service
        - init
       ....
        image: <--- repository url here (e.g. quay.io/nimrodshn/aro-hcp-clusters-service)
        imagePullPolicy: IfNotPresent
```
13. Make sure the cs pods are rotating correctly:
```
kubectl get pods -n clusters-service -w
```
14. To deploy frontend with changes run the following command from the `ARO-HCP/frontend` directory:
```
make deploy
```
15. Observe the pods rotating in your development environment:
```
kubectl get pod -n aro-hcp -w
```
16. To communicate with both frontend and clusters-service recently deployed - 
```
kubectl port-forward svc/aro-hcp-frontend 8443:8443 -n aro-hcp
kubectl port-forward svc/clusters-service 8000:8000 -n clusters-service
```
17. Now you can run the demo scripts [see demo folder under ARO-HCP] 
18. Additionally - run `ocm login --url=http://localhost:8000 --use-auth-code`  and communicate with CS via regular ocm CLI commands.


## Thank you’s
Huge credit and “thank you” to  for helping out and giving me initial guidance and hints in writing this document.