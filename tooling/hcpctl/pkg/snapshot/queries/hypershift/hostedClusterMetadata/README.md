# hypershift / hostedClusterMetadata

## Summary

Resolves the HostedCluster namespace and name on the management cluster from the management cluster content datadump.

## What to Look For

If the RP Backend has collected the HostedCluster through ReadDesires, its
namespace and name should resolve.

| hostedClusterNamespace                          | hostedClusterName |
|-------------------------------------------------|-------------------|
| ocm-arohcpci01-2qa8i5btptoebe3jthlllaecau6mcmpa | ea-cluster        |

## Where to Go Next

If this query is empty, check the backend resource-controller conditions and
ReadDesire state collection.
