# RP ingress HA migration

Tracking: [AROSLSRE-2833](https://redhat.atlassian.net/browse/AROSLSRE-2833).

The legacy AKS external ingress Deployment can colocate its replicas. ARO does
not own that Deployment's desired state. Do not patch it periodically or require
an AKS RP source change to enforce ARO's availability requirement.

The frontend chart can instead define a dedicated Istio Kubernetes Gateway API
gateway and a per-Gateway ConfigMap using the
[supported AKS customization surface](https://learn.microsoft.com/azure/aks/istio-gateway-api#configure-settings-for-a-specific-gateway).
The ConfigMap requires two eligible hostnames, counts only this gateway's pods,
and excludes untolerated nodes from hostname-spread calculations. It also retains
preferred zone spread, an HPA floor of two and a PDB for voluntary disruption.
The PDB is not protection from an unplanned node loss.

## Prepare, validate, activate

Keep both settings false by default. Configure them in the sdp-pipelines HCP
environment/region overlay after consuming the ARO-HCP revision containing this
change. Run the normal service-infrastructure and frontend deployment pipelines
in topology order; do not activate only the frontend against older infrastructure.
Confirm Managed Gateway API is enabled and the Istio add-on is `asm-1-26` or
newer before enabling the feature. The generated resources, customization
admission and upgrade behavior must be validated on the target revision.

1. Prepare with the following configuration and `activate: false`:

   ```yaml
   svc:
     rpIngress:
       enabled: true
       activate: false
       ipAddressName: aro-hcp-rp-ingress
   ```

   Infrastructure provisions a **separate** public IP and allows ARM to reach it
   on 443. The frontend release creates the Gateway, its placement ConfigMap and
   an HTTPRoute. The existing public IP, DNS, Istio Gateway and VirtualService
   remain in service. No two Services compete for the same public IP/port.

2. In INT, verify the generated `rp-ingress-gateway-istio` Deployment/HPA/PDB,
   matching pod selectors, a LoadBalancer address matching the prepared public IP,
   Ready endpoints on distinct nodes, TLS using the
   existing frontend certificate, ARM policy header, and MISE authorization.
   Verify successful and denied requests, normal AKS reconciliation and a minor
   revision upgrade, HPA changes and rolling updates. Perform an explicitly
   authorized unplanned node-loss test;
   a drain alone does not establish node-loss resilience. Check eligible-node
   capacity and hostname/zone labels before promotion.

3. Activate in a **later** rollout by setting `activate: true`. Service
   infrastructure leaves the current DNS record unchanged. After the frontend
   resources are applied, a read-only Helm hook checks Gateway programming,
   Deployment convergence, current HTTPRoute acceptance/backend resolution and
   Ready EndpointSlices on at least two distinct nodes. Hook failure blocks the
   dependent frontend DNS deployment. Only that later ARM step changes the same
   RP hostname's A record to the prepared public IP (TTL 300).

   This Kubernetes readiness gate does not replace the TLS/MISE/ARM traffic
   validation in the preparation phase.

The hostname, certificate and backend route are preserved; the public IP changes
at activation. Keep the legacy endpoint available while DNS caches expire. Do
not remove its IP, listener or routing as part of this rollout. The ops ingress
gateway and its network access remain separate and unchanged.

## Rollback

Set `activate: false` and redeploy service infrastructure/frontend in normal
order. Infrastructure restores the legacy DNS address, while the prepared
Gateway API endpoint remains available for cached new-IP traffic. Do not turn
`enabled` off until the DNS TTL has expired and traffic has been verified on the
legacy endpoint. Do not delete the new public IP or gateway during that drain.

Both paths continue to use the frontend's existing authorization policies. The
readiness hook has only Kubernetes read permissions and no Azure/DNS identity;
DNS changes use the deployment identity in the existing pipeline.

This procedure and rendered tests do not constitute a completed live failover
test. No Azure/Kubernetes resource change is required merely to review the PR.
