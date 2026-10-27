# Kube-applier throughput registration

Registration writes ManagementCluster documents directly to Fleet Cosmos, not
through AdminAPI. The chart and kube-applier Cosmos provisioning parameters both
read `kubeApplier.cosmosContainerMaxScale`; registration records that value as
`status.kubeApplierCosmosContainerMaxScale` alongside the physical container name.
This records provisioning configuration, not a live ARM measurement.

All management clusters must have a positive registered maximum before the
backend consumer is enabled. All document writers, including Fleet, AdminAPI,
and registration, must preserve the field. The registration pipeline waits for
Fleet and AdminAPI deployment; operators must also use an upgraded registration
image. Older writers can otherwise discard the field when replacing documents.

Validated creates and updates require a value of at least one, including updates
that leave an invalid value unchanged. An omitted int32 field decodes to zero and
is rejected. Rerunning registration populates or updates the value. Get and List
do not validate stored records; raw out-of-band Cosmos writes are outside this
write guard. Zero must not mean unlimited capacity, a hardcoded replacement, or
an ARM fallback.

The backend takes a first-success, process-lifetime budget snapshot per physical
container. After changing provisioned throughput, rerun registration and restart
the backend to refresh successful cached budgets. Coordinate decreases so backend
instances do not continue using an older, larger budget after capacity is reduced.
Registration does not detect out-of-band ARM throughput changes automatically.
