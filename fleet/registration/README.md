# Kube-applier throughput registration rollout

Registration writes ManagementCluster documents directly to Fleet Cosmos, not
through AdminAPI. The chart and the kube-applier Cosmos provisioning parameters
both read `kubeApplier.cosmosContainerMaxScale`; registration records that value
as `status.kubeApplierCosmosContainerMaxScale` alongside the physical container
name. This records provisioning configuration, not a live ARM measurement.

The registration change must merge and roll out to production before the backend
consumer change in PR #7730 may merge or deploy. Upgrade all ManagementCluster
document writers, including Fleet and AdminAPI, before rerunning registration
for existing management clusters. Older writers can otherwise discard the new
field when replacing documents. The pipeline waits for Fleet and AdminAPI
deployment; operators must also ensure the upgraded registration image is used.

Verify that every existing management cluster has a positive registered maximum
before enabling the consumer. Untouched legacy zero values remain legal for
unrelated updates, but are not valid throughput budgets. Registration requires a
positive value, and rerunning registration backfills legacy records. Zero must
not be interpreted as unlimited capacity or replaced with an ARM fallback.

The backend consumer takes a first-success, process-lifetime snapshot per physical
container. After changing provisioned throughput, rerun registration and restart
the backend to refresh successful cached budgets. Coordinate decreases so backend
instances do not continue using an older, larger budget after capacity is reduced.
Registration does not detect out-of-band ARM throughput changes automatically.
