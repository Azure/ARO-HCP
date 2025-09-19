# Postgres Breakglass

This guide describes how to access the Postgres database of ARO HCP service, specifically to the Cluster Service and Maestro Azure Postgres flexibleserver databases.

## Process

1. Get access to the service cluster hosting the respective service
2. Scale up the postgress-breakglass deployment in the respective service component namespace
   ```/bin/sh
   kubectl scale deployment postgres-breakglass -n clusters-service --replicas=1
   or
   kubectl scale deployment postgres-breakglass -n maestro --replicas=1
   ```
   Wait for the deployment to be ready
1. Exec into the pod of the deployment

   ```/bin/sh
   kubectl exec -ti (kubectl get pods -n  clusters-service -l app=postgres-breakglass -o name) -n clusters-service -- /bin/bash
   or
   kubectl exec -ti (kubectl get pods -n  clusters-service -l app=postgres-breakglass -o name) -n maestro -- /bin/bash
   ```

2. Run `connect` within the container to start a `psql` session
3. Once done, exit the container and scale down the deployment

   ```/bin/sh
   kubectl scale deployment postgres-breakglass -n clusters-service --replicas=0
   or
   kubectl scale deployment postgres-breakglass -n maestro --replicas=0
   ```

> [!IMPORTANT]
> The pod can only be used for about 1h after creation to establish new postgres sessions. After that the temporary credentials that have been minted at startup are stale. Recycle the pod in this case. Existing postgres sessions will continue to work.
