<map version="1.0.1">
<!-- To view this file, download free mind mapping software FreeMind from http://freemind.sourceforge.net -->
<node CREATED="1742324400000" ID="ID_1000000" MODIFIED="1742324400000" TEXT="ARO-HCP Resource Creation">
<node CREATED="1742324401000" ID="ID_1000001" MODIFIED="1742324401000" POSITION="right" TEXT="Resource types">
<node CREATED="1742324402000" ID="ID_1000002" MODIFIED="1742324402000" TEXT="HCPOpenShiftCluster (parent resource)"/>
<node CREATED="1742324403000" ID="ID_1000003" MODIFIED="1742324403000" TEXT="NodePool (child of cluster)"/>
<node CREATED="1742324404000" ID="ID_1000004" MODIFIED="1742324404000" TEXT="ExternalAuth (child of cluster)"/>
</node>
<node CREATED="1742324405000" ID="ID_1000005" MODIFIED="1742324405000" POSITION="right" TEXT="Frontend middleware chain (frontend/pkg/frontend/routes.go)">
<node CREATED="1742324406000" ID="ID_1000006" MODIFIED="1742324406000" TEXT="pre-mux middleware (all requests)">
<node CREATED="1742324407000" ID="ID_1000007" MODIFIED="1742324407000" TEXT="MiddlewarePanic - recover from panics"/>
<node CREATED="1742324408000" ID="ID_1000008" MODIFIED="1742324408000" TEXT="MiddlewareReferer - ensure Referer header for pagination"/>
<node CREATED="1742324409000" ID="ID_1000009" MODIFIED="1742324409000" TEXT="Metrics - request metrics collection"/>
<node CREATED="1742324410000" ID="ID_1000010" MODIFIED="1742324410000" TEXT="MiddlewareCorrelationData - ARM correlation tracking"/>
<node CREATED="1742324411000" ID="ID_1000011" MODIFIED="1742324411000" TEXT="Audit - audit logging"/>
<node CREATED="1742324412000" ID="ID_1000012" MODIFIED="1742324412000" TEXT="MiddlewareTracing - distributed tracing (OpenTelemetry)"/>
<node CREATED="1742324413000" ID="ID_1000013" MODIFIED="1742324413000" TEXT="MiddlewareLowercase - lowercase URL for case-insensitive routing"/>
<node CREATED="1742324414000" ID="ID_1000014" MODIFIED="1742324414000" TEXT="MiddlewareLogging - request logging"/>
<node CREATED="1742324415000" ID="ID_1000015" MODIFIED="1742324415000" TEXT="MiddlewarePanic - second recovery (after tracing setup)"/>
<node CREATED="1742324416000" ID="ID_1000016" MODIFIED="1742324416000" TEXT="MiddlewareBody - read and cache request body in context"/>
<node CREATED="1742324417000" ID="ID_1000017" MODIFIED="1742324417000" TEXT="MiddlewareSystemData - extract ARM SystemData from headers"/>
</node>
<node CREATED="1742324418000" ID="ID_1000018" MODIFIED="1742324418000" TEXT="ServeMux pattern matching"/>
<node CREATED="1742324419000" ID="ID_1000019" MODIFIED="1742324419000" TEXT="post-mux middleware (varies by endpoint type)">
<node CREATED="1742324460100" ID="ID_1000160" MODIFIED="1742324460100" TEXT="list endpoints: LoggingPostMux, ValidateAPIVersion, ValidateSubscriptionState"/>
<node CREATED="1742324460200" ID="ID_1000161" MODIFIED="1742324460200" TEXT="read endpoints: ResourceID, LoggingPostMux, ValidateAPIVersion, ValidateSubscriptionState"/>
<node CREATED="1742324460300" ID="ID_1000162" MODIFIED="1742324460300" TEXT="create/update/delete: ResourceID, LoggingPostMux, ValidateAPIVersion, LockSubscription, ValidateSubscriptionState"/>
<node CREATED="1742324460400" ID="ID_1000163" MODIFIED="1742324460400" TEXT="operation endpoints: ResourceID, LoggingPostMux, ValidateAPIVersion, ValidateSubscriptionState"/>
<node CREATED="1742324460500" ID="ID_1000164" MODIFIED="1742324460500" TEXT="subscription mgmt: ResourceID, LoggingPostMux (PUT adds LockSubscription)"/>
</node>
</node>
<node CREATED="1742324425000" ID="ID_1000025" MODIFIED="1742324425000" POSITION="right" TEXT="HCPOpenShiftCluster creation (frontend/pkg/frontend/cluster.go)">
<node CREATED="1742324426000" ID="ID_1000026" MODIFIED="1742324426000" TEXT="PUT handled by CreateOrUpdateHCPCluster">
<node CREATED="1742324427000" ID="ID_1000027" MODIFIED="1742324427000" TEXT="check if resource exists in CosmosDB"/>
<node CREATED="1742324428000" ID="ID_1000028" MODIFIED="1742324428000" TEXT="if exists: checkForProvisioningStateConflict, then update or patch"/>
<node CREATED="1742324429000" ID="ID_1000029" MODIFIED="1742324429000" TEXT="if new: createHCPCluster()"/>
</node>
<node CREATED="1742324430000" ID="ID_1000030" MODIFIED="1742324430000" TEXT="createHCPCluster flow">
<node CREATED="1742324431000" ID="ID_1000031" MODIFIED="1742324431000" TEXT="get subscription from CosmosDB"/>
<node CREATED="1742324432000" ID="ID_1000032" MODIFIED="1742324432000" TEXT="decodeDesiredClusterCreate">
<node CREATED="1742324433000" ID="ID_1000033" MODIFIED="1742324433000" TEXT="unmarshal request body to versioned external type"/>
<node CREATED="1742324434000" ID="ID_1000034" MODIFIED="1742324434000" TEXT="set default values for API version"/>
<node CREATED="1742324435000" ID="ID_1000035" MODIFIED="1742324435000" TEXT="convert to internal type"/>
<node CREATED="1742324436000" ID="ID_1000036" MODIFIED="1742324436000" TEXT="set TrackedResource fields (ID, location)"/>
<node CREATED="1742324437000" ID="ID_1000037" MODIFIED="1742324437000" TEXT="set SystemData (createdAt, createdBy)"/>
<node CREATED="1742324438000" ID="ID_1000038" MODIFIED="1742324438000" TEXT="set MSI data plane identity URL from X-Ms-Identity-Url header"/>
</node>
<node CREATED="1742324439000" ID="ID_1000039" MODIFIED="1742324439000" TEXT="MutateCluster - admission mutations"/>
<node CREATED="1742324440000" ID="ID_1000040" MODIFIED="1742324440000" TEXT="ValidateCluster - static validation">
<node CREATED="1742324441000" ID="ID_1000041" MODIFIED="1742324441000" TEXT="uses AFEC feature flags from subscription for validation options"/>
</node>
<node CREATED="1742324442000" ID="ID_1000042" MODIFIED="1742324442000" TEXT="AdmitClusterOnCreate - admission checks"/>
<node CREATED="1742324443000" ID="ID_1000043" MODIFIED="1742324443000" TEXT="BuildCSCluster - build Cluster Service request">
<node CREATED="1742324444000" ID="ID_1000044" MODIFIED="1742324444000" TEXT="set provision shard if configured"/>
<node CREATED="1742324445000" ID="ID_1000045" MODIFIED="1742324445000" TEXT="set noop provision/deprovision flags if configured"/>
</node>
<node CREATED="1742324446000" ID="ID_1000046" MODIFIED="1742324446000" TEXT="clusterServiceClient.PostCluster - create in Cluster Service"/>
<node CREATED="1742324447000" ID="ID_1000047" MODIFIED="1742324447000" TEXT="store ClusterServiceID (HREF) from CS response"/>
<node CREATED="1742324448000" ID="ID_1000048" MODIFIED="1742324448000" TEXT="create CosmosDB transaction">
<node CREATED="1742324449000" ID="ID_1000049" MODIFIED="1742324449000" TEXT="create operation document (OperationRequestCreate)">
<node CREATED="1742324450000" ID="ID_1000050" MODIFIED="1742324450000" TEXT="captures tenant ID, client object ID from headers"/>
<node CREATED="1742324451000" ID="ID_1000051" MODIFIED="1742324451000" TEXT="captures async notification URI from ARM"/>
<node CREATED="1742324452000" ID="ID_1000052" MODIFIED="1742324452000" TEXT="captures correlation data"/>
</node>
<node CREATED="1742324453000" ID="ID_1000053" MODIFIED="1742324453000" TEXT="set ActiveOperationID and ProvisioningState on cluster"/>
<node CREATED="1742324454000" ID="ID_1000054" MODIFIED="1742324454000" TEXT="create cluster document in CosmosDB"/>
<node CREATED="1742324455000" ID="ID_1000055" MODIFIED="1742324455000" TEXT="execute transaction atomically"/>
</node>
<node CREATED="1742324456000" ID="ID_1000056" MODIFIED="1742324456000" TEXT="merge Cluster Service response with CosmosDB document"/>
<node CREATED="1742324457000" ID="ID_1000057" MODIFIED="1742324457000" TEXT="return 201 Created with Azure-AsyncOperation and Location headers"/>
</node>
</node>
<node CREATED="1742324458000" ID="ID_1000058" MODIFIED="1742324458000" POSITION="right" TEXT="NodePool creation (frontend/pkg/frontend/node_pool.go)">
<node CREATED="1742324459000" ID="ID_1000059" MODIFIED="1742324459000" TEXT="PUT handled by CreateOrUpdateNodePool">
<node CREATED="1742324460000" ID="ID_1000060" MODIFIED="1742324460000" TEXT="check if node pool exists in CosmosDB"/>
<node CREATED="1742324461000" ID="ID_1000061" MODIFIED="1742324461000" TEXT="if exists: checkForProvisioningStateConflict, then update or patch"/>
<node CREATED="1742324462000" ID="ID_1000062" MODIFIED="1742324462000" TEXT="if new: createNodePool()"/>
</node>
<node CREATED="1742324463000" ID="ID_1000063" MODIFIED="1742324463000" TEXT="createNodePool flow">
<node CREATED="1742324464000" ID="ID_1000064" MODIFIED="1742324464000" TEXT="decodeDesiredNodePoolCreate">
<node CREATED="1742324465000" ID="ID_1000065" MODIFIED="1742324465000" TEXT="unmarshal request body to versioned external type"/>
<node CREATED="1742324466000" ID="ID_1000066" MODIFIED="1742324466000" TEXT="set default values for API version"/>
<node CREATED="1742324467000" ID="ID_1000067" MODIFIED="1742324467000" TEXT="convert to internal type"/>
<node CREATED="1742324468000" ID="ID_1000068" MODIFIED="1742324468000" TEXT="set TrackedResource fields and SystemData"/>
</node>
<node CREATED="1742324469000" ID="ID_1000069" MODIFIED="1742324469000" TEXT="getInternalClusterFromStorage - fetch parent cluster for validation"/>
<node CREATED="1742324470000" ID="ID_1000070" MODIFIED="1742324470000" TEXT="ValidateNodePoolCreate - static validation"/>
<node CREATED="1742324471000" ID="ID_1000071" MODIFIED="1742324471000" TEXT="AdmitNodePool - admission checks against parent cluster"/>
<node CREATED="1742324472000" ID="ID_1000072" MODIFIED="1742324472000" TEXT="checkForProvisioningStateConflict"/>
<node CREATED="1742324473000" ID="ID_1000073" MODIFIED="1742324473000" TEXT="BuildCSNodePool - build Cluster Service request"/>
<node CREATED="1742324474000" ID="ID_1000074" MODIFIED="1742324474000" TEXT="clusterServiceClient.PostNodePool - create in Cluster Service"/>
<node CREATED="1742324475000" ID="ID_1000075" MODIFIED="1742324475000" TEXT="store ClusterServiceID (HREF) from CS response"/>
<node CREATED="1742324476000" ID="ID_1000076" MODIFIED="1742324476000" TEXT="create CosmosDB transaction">
<node CREATED="1742324477000" ID="ID_1000077" MODIFIED="1742324477000" TEXT="create operation document (OperationRequestCreate)"/>
<node CREATED="1742324478000" ID="ID_1000078" MODIFIED="1742324478000" TEXT="set ActiveOperationID and ProvisioningState on node pool"/>
<node CREATED="1742324479000" ID="ID_1000079" MODIFIED="1742324479000" TEXT="create node pool document in CosmosDB"/>
<node CREATED="1742324480000" ID="ID_1000080" MODIFIED="1742324480000" TEXT="execute transaction atomically"/>
</node>
<node CREATED="1742324481000" ID="ID_1000081" MODIFIED="1742324481000" TEXT="merge Cluster Service response with CosmosDB document"/>
<node CREATED="1742324482000" ID="ID_1000082" MODIFIED="1742324482000" TEXT="return 201 Created with Azure-AsyncOperation and Location headers"/>
</node>
</node>
<node CREATED="1742324483000" ID="ID_1000083" MODIFIED="1742324483000" POSITION="right" TEXT="Backend: async operation processing (backend/pkg/controllers)">
<node CREATED="1742324484000" ID="ID_1000084" MODIFIED="1742324484000" TEXT="controller architecture">
<node CREATED="1742324485000" ID="ID_1000085" MODIFIED="1742324485000" TEXT="Kubernetes-style controllers running on service cluster"/>
<node CREATED="1742324486000" ID="ID_1000086" MODIFIED="1742324486000" TEXT="SharedInformers backed by CosmosDB via periodic relist (expiring watcher, not native change feed)"/>
<node CREATED="1742324487000" ID="ID_1000087" MODIFIED="1742324487000" TEXT="rate-limited work queue with 10-second cooldown between syncs"/>
<node CREATED="1742324488000" ID="ID_1000088" MODIFIED="1742324488000" TEXT="operation controllers use ShouldProcess filter (request type + resource type)"/>
</node>
<node CREATED="1742324489000" ID="ID_1000089" MODIFIED="1742324489000" TEXT="operation controllers (poll Cluster Service status)">
<node CREATED="1742324490000" ID="ID_1000090" MODIFIED="1742324490000" TEXT="Cluster Create - polls GetClusterStatus, creates billing doc on success"/>
<node CREATED="1742324491000" ID="ID_1000091" MODIFIED="1742324491000" TEXT="Cluster Update - polls GetClusterStatus"/>
<node CREATED="1742324492000" ID="ID_1000092" MODIFIED="1742324492000" TEXT="Cluster Delete - polls GetClusterStatus, deletes billing and resource docs"/>
<node CREATED="1742324493000" ID="ID_1000093" MODIFIED="1742324493000" TEXT="NodePool Create - polls GetNodePoolStatus"/>
<node CREATED="1742324494000" ID="ID_1000094" MODIFIED="1742324494000" TEXT="NodePool Update - polls GetNodePoolStatus"/>
<node CREATED="1742324495000" ID="ID_1000095" MODIFIED="1742324495000" TEXT="NodePool Delete - polls GetNodePoolStatus, deletes resource doc"/>
<node CREATED="1742324496000" ID="ID_1000096" MODIFIED="1742324496000" TEXT="ExternalAuth Create/Update/Delete"/>
<node CREATED="1742324497000" ID="ID_1000097" MODIFIED="1742324497000" TEXT="Request Credential / Revoke Credentials"/>
</node>
<node CREATED="1742324498000" ID="ID_1000098" MODIFIED="1742324498000" TEXT="SynchronizeOperation flow">
<node CREATED="1742324499000" ID="ID_1000099" MODIFIED="1742324499000" TEXT="get operation document from CosmosDB"/>
<node CREATED="1742324500000" ID="ID_1000100" MODIFIED="1742324500000" TEXT="poll Cluster Service for resource status"/>
<node CREATED="1742324501000" ID="ID_1000101" MODIFIED="1742324501000" TEXT="convert CS state to ARM ProvisioningState">
<node CREATED="1742324503000" ID="ID_1000103" MODIFIED="1742324503000" TEXT="installing -> Provisioning"/>
<node CREATED="1742324504000" ID="ID_1000104" MODIFIED="1742324504000" TEXT="updating -> Updating"/>
<node CREATED="1742324505000" ID="ID_1000105" MODIFIED="1742324505000" TEXT="ready -> Succeeded (non-delete only; ready during delete is ignored)"/>
<node CREATED="1742324506000" ID="ID_1000106" MODIFIED="1742324506000" TEXT="error -> Failed (with ProvisionErrorCode and message)"/>
<node CREATED="1742324507000" ID="ID_1000107" MODIFIED="1742324507000" TEXT="uninstalling -> Deleting"/>
<node CREATED="1742324507100" ID="ID_1000165" MODIFIED="1742324507100" TEXT="pending / validating -> tolerated only when already Accepted (no active transition)"/>
<node CREATED="1742324507200" ID="ID_1000166" MODIFIED="1742324507200" TEXT="delete success determined by 404 Not Found from CS, not ready state"/>
</node>
<node CREATED="1742324508000" ID="ID_1000108" MODIFIED="1742324508000" TEXT="(cluster create only) create billing document on success"/>
<node CREATED="1742324509000" ID="ID_1000109" MODIFIED="1742324509000" TEXT="UpdateOperationStatus - atomic CosmosDB transaction">
<node CREATED="1742324510000" ID="ID_1000110" MODIFIED="1742324510000" TEXT="update operation document (status, lastTransitionTime, error)"/>
<node CREATED="1742324511000" ID="ID_1000111" MODIFIED="1742324511000" TEXT="update resource document (ProvisioningState, ActiveOperationID)"/>
<node CREATED="1742324512000" ID="ID_1000112" MODIFIED="1742324512000" TEXT="POST async notification to ARM if notification URI present"/>
</node>
</node>
<node CREATED="1742324513000" ID="ID_1000113" MODIFIED="1742324513000" TEXT="validation controllers (async checks)">
<node CREATED="1742324514000" ID="ID_1000114" MODIFIED="1742324514000" TEXT="azure_cluster_resource_group_existence_validation"/>
<node CREATED="1742324515000" ID="ID_1000115" MODIFIED="1742324515000" TEXT="azure_rp_registration_validation"/>
<node CREATED="1742324516000" ID="ID_1000116" MODIFIED="1742324516000" TEXT="azure_cluster_mis_existence_validation (managed identity)"/>
</node>
<node CREATED="1742324517000" ID="ID_1000117" MODIFIED="1742324517000" TEXT="other controllers">
<node CREATED="1742324518000" ID="ID_1000118" MODIFIED="1742324518000" TEXT="mismatch controllers - reconcile Cosmos vs Cluster Service state"/>
<node CREATED="1742324519000" ID="ID_1000119" MODIFIED="1742324519000" TEXT="upgrade controllers - trigger control plane upgrades"/>
<node CREATED="1742324520000" ID="ID_1000120" MODIFIED="1742324520000" TEXT="cluster properties sync - identity migration, customer properties"/>
<node CREATED="1742324521000" ID="ID_1000121" MODIFIED="1742324521000" TEXT="Maestro bundle controllers - create/delete readonly resource bundles"/>
</node>
</node>
<node CREATED="1742324522000" ID="ID_1000122" MODIFIED="1742324522000" POSITION="right" TEXT="Cluster Service and Maestro">
<node CREATED="1742324523000" ID="ID_1000123" MODIFIED="1742324523000" TEXT="Cluster Service (runs on service cluster)">
<node CREATED="1742324524000" ID="ID_1000124" MODIFIED="1742324524000" TEXT="receives POST from frontend to create cluster/nodepool"/>
<node CREATED="1742324525000" ID="ID_1000125" MODIFIED="1742324525000" TEXT="validates cluster/nodepool spec"/>
<node CREATED="1742324526000" ID="ID_1000126" MODIFIED="1742324526000" TEXT="persists to CS database"/>
<node CREATED="1742324527000" ID="ID_1000127" MODIFIED="1742324527000" TEXT="orchestrates provisioning"/>
<node CREATED="1742324528000" ID="ID_1000128" MODIFIED="1742324528000" TEXT="sends resource bundles to management cluster via Maestro"/>
</node>
<node CREATED="1742324529000" ID="ID_1000129" MODIFIED="1742324529000" TEXT="Maestro (runs on service cluster)">
<node CREATED="1742324530000" ID="ID_1000130" MODIFIED="1742324530000" TEXT="multi-cluster orchestration layer"/>
<node CREATED="1742324531000" ID="ID_1000131" MODIFIED="1742324531000" TEXT="delivers resource bundles to management clusters"/>
<node CREATED="1742324532000" ID="ID_1000132" MODIFIED="1742324532000" TEXT="reports status back to Cluster Service"/>
</node>
<node CREATED="1742324533000" ID="ID_1000133" MODIFIED="1742324533000" TEXT="Management Cluster">
<node CREATED="1742324534000" ID="ID_1000134" MODIFIED="1742324534000" TEXT="deploys Hosted Control Plane (etcd, API server, controllers)"/>
<node CREATED="1742324535000" ID="ID_1000135" MODIFIED="1742324535000" TEXT="node pool machines run in customer VNet"/>
</node>
<node CREATED="1742324536000" ID="ID_1000136" MODIFIED="1742324536000" TEXT="Cluster Service status lifecycle">
<node CREATED="1742324537000" ID="ID_1000137" MODIFIED="1742324537000" TEXT="pending"/>
<node CREATED="1742324538000" ID="ID_1000138" MODIFIED="1742324538000" TEXT="validating"/>
<node CREATED="1742324539000" ID="ID_1000139" MODIFIED="1742324539000" TEXT="installing"/>
<node CREATED="1742324540000" ID="ID_1000140" MODIFIED="1742324540000" TEXT="ready (success) or error (failure)"/>
</node>
</node>
<node CREATED="1742324541000" ID="ID_1000141" MODIFIED="1742324541000" POSITION="right" TEXT="Async operation lifecycle">
<node CREATED="1742324542000" ID="ID_1000142" MODIFIED="1742324542000" TEXT="Frontend creates operation document -> Accepted"/>
<node CREATED="1742324543000" ID="ID_1000143" MODIFIED="1742324543000" TEXT="CS begins installing -> Provisioning"/>
<node CREATED="1742324544000" ID="ID_1000144" MODIFIED="1742324544000" TEXT="CS reports ready -> Succeeded (terminal)"/>
<node CREATED="1742324545000" ID="ID_1000145" MODIFIED="1742324545000" TEXT="CS reports error -> Failed (terminal)"/>
<node CREATED="1742324546000" ID="ID_1000146" MODIFIED="1742324546000" TEXT="client polls via Azure-AsyncOperation URL"/>
<node CREATED="1742324547000" ID="ID_1000147" MODIFIED="1742324547000" TEXT="backend controllers poll CS every ~10 seconds"/>
</node>
<node CREATED="1742324548000" ID="ID_1000148" MODIFIED="1742324548000" POSITION="right" TEXT="Key differences from classic ARO-RP">
<node CREATED="1742324549000" ID="ID_1000149" MODIFIED="1742324549000" TEXT="control plane is hosted on management cluster, not customer VMs"/>
<node CREATED="1742324550000" ID="ID_1000150" MODIFIED="1742324550000" TEXT="backend delegates to Cluster Service instead of running install steps directly"/>
<node CREATED="1742324551000" ID="ID_1000151" MODIFIED="1742324551000" TEXT="Cluster Service + Maestro orchestrate provisioning (not Hive/Podman)"/>
<node CREATED="1742324552000" ID="ID_1000152" MODIFIED="1742324552000" TEXT="separate operation documents in CosmosDB (not just provisioning state on cluster)"/>
<node CREATED="1742324553000" ID="ID_1000153" MODIFIED="1742324553000" TEXT="periodic relist via expiring watchers triggers Kubernetes-style controllers (not lease-based dequeue)"/>
<node CREATED="1742324554000" ID="ID_1000154" MODIFIED="1742324554000" TEXT="NodePool and ExternalAuth are first-class ARM resources with own lifecycles"/>
</node>
</node>
</map>
