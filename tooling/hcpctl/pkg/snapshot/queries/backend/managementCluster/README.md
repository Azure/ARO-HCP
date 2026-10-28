# backend / managementCluster

## Summary

Resolves the AKS management cluster that actually hosts a cluster's control plane by joining its live ReadDesire to the fleet management-cluster inventory.

## What to Look For

This query must return exactly one management cluster. An empty result means placement cannot be scoped, and an ambiguous result means the resource maps to multiple management clusters.
