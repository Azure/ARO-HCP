# backend / cosmosResourceDiscovery

## Summary

Discovers all resource types that exist in Cosmos DB under the cluster's ARM resource ID prefix.
This drives which per-resource queries are applicable (e.g., controller conditions only fire when
`hcpopenshiftcontrollers` child documents exist).

## What to Look For

A list of `(resourceID, resourceType)` pairs showing all Cosmos documents related to this cluster.
Resource types with `/hcpopenshiftcontrollers` suffixes indicate controller condition data is available.
Resource types with `/readdesires` indicate management cluster state is available. Resource types with
`/serviceprovider*` indicate service provider state documents exist.

## Where to Go Next

This query is informational — its results drive the availability of downstream queries like
`backend/resourceControllerConditions`, `backend/serviceProviderState`, and `hypershift/hostedClusterMetadata`.
