# Fix Maestro Stale Resource Bundle

## Problem Description

When resources in a Management Cluster (MC) are recreated or modified, Maestro may not update the resource bundle with the new information, causing dependent services to use stale data. This can occur with the shared ingress service on MC reinstallation but can affect any resource that Maestro observes.

## Common Scenarios

- **Shared Ingress IP Change**: When MC is recreated, the shared ingress service gets a new IP, but CS continues using the old IP for DNS records
- **Any observed resource**: Where Maestro's resource bundle contains outdated information

## Symptoms

- API server connection failures (for shared ingress issues)
- DNS resolution pointing to outdated endpoints (for shared ingress issues)
- Mismatch between actual resource state and what Maestro reports

## Root Cause

The Maestro agent only reports resource changes when the resource generation is incremented. If a resource is recreated and was previously reported with resourceVersion: 0, the new one will as well and Maestro doesn't detect it as a change and continues reporting stale data to the server.

## Scenario: recreate MGMT cluster and shared ingress reports wrong IP

1. **Identify the stale resource**
   
   For ingress/service issues:
   ```bash
   # Check DNS resolution for the API server
   nslookup api.<cluster-name>.<region>.aroapp-hcp.azure-test.net
   
   # Check the actual shared ingress service IP in the MC
   kubectl get svc -n hypeshift-sharedingress router
   ```

2. **Verify stale data in Maestro**
   ```bash
   # Port-forward to Maestro server (port 8002)
   kubectl port-forward -n maestro deployment/maestro-server 8002:8002
   
   # Check for old value in resource bundles
   curl -sG http://localhost:8002/api/maestro/v1/resource-bundles | grep <old-value>
   
   # Verify new value is not present
   curl -sG http://localhost:8002/api/maestro/v1/resource-bundles | grep <new-value>
   
   # Or examine the full resource bundle
   curl -sG http://localhost:8002/api/maestro/v1/resource-bundles | jq
   ```

## Resolution Steps

### Step 1: Force Resource Generation Update

To trigger Maestro to update the resource bundle, modify the resource's spec to increment its generation. The modification must be in the spec section, as metadata changes (labels/annotations) won't trigger a generation update.

**For Services:**
```bash
# Edit the service
oc edit svc -n <namespace> <service-name>
```

Add a dummy port to the spec:
```yaml
spec:
  ports:
  # ... existing ports ...
  - name: dummy-update
    port: 9999
    protocol: TCP
    targetPort: <any-valid-target>
```


### Step 2: Restart Maestro Agent (Optional)

To speed up the update process:
```bash
kubectl rollout restart -n maestro deployment/maestro-agent
```

### Step 3: Verify Update

```bash
# Check that Maestro now has the correct value
curl -sG http://localhost:8002/api/maestro/v1/resource-bundles | grep <new-value>

# Or verify the specific resource bundle
curl -sG http://localhost:8002/api/maestro/v1/resource-bundles | jq '.items[] | select(.metadata.name | contains("<resource-identifier>"))'
```

### Step 4: Handle Downstream Dependencies

**Important**: Depending on the resource type and how it's consumed:

- **For DNS records created by CS**: These are not automatically updated. Affected HCP clusters must be deleted and recreated to pick up the new values
- **For configuration changes**: Services consuming the data may need to be restarted
- **For endpoint changes**: Update any hardcoded references or trigger reconciliation in dependent services

## Related Components

- **Maestro**: Resource bundle management system that observes and reports resource state
- **Cluster Service (CS)**: Consumes Maestro data for various operations including DNS management
- **Management Cluster (MC)**: Hosts observed resources
- **HyperShift Control Plane (HCP)**: May be affected by stale resource data
