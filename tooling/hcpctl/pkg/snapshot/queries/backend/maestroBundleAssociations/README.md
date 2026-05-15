# backend / maestroBundleAssociations

## Summary

Extracts Maestro resource bundle associations from the service provider resource, mapping bundle IDs and names to the
resources they contain.

## What to Look For

A list of resources with the bundles that contain them:

| bundleName                      | maestroBundleName | maestroBundleId | groupVersion                    | resourceKind  | resourceNamespace | resourceName |
|---------------------------------|-------------------|-----------------|---------------------------------|---------------|-------------------|--------------|
| readonlyHypershiftHostedCluster | uuid              | uuid            | hypershift.openshift.io/v1beta1 | HostedCluster | ocm-arohcpint-cid | name         |

## Where to Go Next

If no resources are present, check the Maestro-related controller conditions.
