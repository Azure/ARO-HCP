# Introduce New Services

To introduce new services into the ARO HCP architecture, follow the steps outlines below.

## Infrastructure

Figure out if the new service requires additional Azure infrastructure (PAAS, managed identities, permissions, ...). Read about the [ARO HCP High Level Architecture](high-level-architecture.md) to understand what architectural scope this new infrastructure falls under. Then add the new infrastructure to the project bicep templates as described in the [Bicep documentation](bicep.md).

## Helm Chart

If the new services runs on an AKS cluster, it needs to be packaged as a Helm Chart. Read the [Helm Chart documentation](service-deployment-concept.md#helm-chart) to understand how to create a Helm Chart for the service.

## Pipeline Definition

Rollouts for infrastructure and services are defined in pipeline files. The [Pipeline Concept documentation](pipeline-concept.md) describes how to create a new pipeline definition.

## Deploy to Red Hat Environments

Once a pipeline file is defined, it can be used to deploy the Red Hat environments using the [Red Hat Pipeline Runner](pipeline-concept.md#red-hat-pipeline-runner).

## Topology Registration

Pipeline files are organized in a topology tree that reflects the ARO HCP architecture. The [Pipeline Topology documentation](pipeline-topology.md) describes how to add a new pipeline to the topology and how to build ADO pipelines from it.

## Deploy to MSFT Environments

Once a new service pipeline is registered with ADO and EV2, the [EV2 Deployment documentation](ev2-deployment.md) describes how to run the pipeline and deploy the new service into the ARO HCP environment.
