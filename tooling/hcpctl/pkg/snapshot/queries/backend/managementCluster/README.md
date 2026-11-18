# backend / managementCluster

## Summary

Resolves the AKS management clusters that host the control planes for all clusters referenced by frontend requests. It uses each ReadDesire's `spec.targetItem` and `spec.managementCluster`, so placement survives HostedCluster cleanup states where `status.kubeContent` is cleared or the ReadDesire is soft-deleted, then joins the placement to the fleet management-cluster inventory.

## What to Look For

This query must return exactly one management cluster. An empty result means placement cannot be scoped, and an ambiguous result means the resource maps to multiple management clusters.
