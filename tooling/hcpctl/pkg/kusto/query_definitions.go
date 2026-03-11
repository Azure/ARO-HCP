package kusto

// QueryDefinition declaratively describes a single KQL query.
// Simple queries can be built entirely from a definition via QueryFactory.Build().
type QueryDefinition struct {
	Name          string
	Table         string
	Database      string
	TemplatePath  string
	ProjectFields []string // nil = no project field substitution
}

// Query definitions for component queries.
var (
	DebugQueriesQuery = QueryDefinition{
		Name:          "debugQueries",
		Table:         ContainerLogsTable,
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/debug_queries.kql.gotmpl",
		ProjectFields: []string{"timestamp", "log"},
	}
	BackendLogsQuery = QueryDefinition{
		Name:          "backendLogs",
		Table:         "backendLogs",
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/backend_logs.kql.gotmpl",
		ProjectFields: []string{"timestamp", "msg", "log"},
	}
	BackendControllerConditionsQuery = QueryDefinition{
		Name:         "backendControllerConditions",
		Table:        "backendLogs",
		Database:     ServicesDatabase,
		TemplatePath: "templates/components/backend_controller_conditions.kql.gotmpl",
	}
	FrontendLogsQuery = QueryDefinition{
		Name:          "frontendLogs",
		Table:         "frontendLogs",
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/frontend_logs.kql.gotmpl",
		ProjectFields: []string{"timestamp", "msg", "log"},
	}
	ClustersServiceLogsQuery = QueryDefinition{
		Name:          "clustersServiceLogs",
		Table:         ContainerLogsTable,
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/clusters_service_logs.kql.gotmpl",
		ProjectFields: []string{"timestamp", "log"},
	}
	ClustersServicePhasesQuery = QueryDefinition{
		Name:          "clustersServicePhases",
		Table:         ContainerLogsTable,
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/clusters_service_phases.kql.gotmpl",
		ProjectFields: []string{"timestamp", "log"},
	}
	MaestroLogsQuery = QueryDefinition{
		Name:          "maestroLogs",
		Table:         ContainerLogsTable,
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/maestro_logs.kql.gotmpl",
		ProjectFields: []string{"timestamp", "container_name", "log"},
	}
	HypershiftLogsQuery = QueryDefinition{
		Name:          "hypershiftLogs",
		Table:         ContainerLogsTable,
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/hypershift_logs.kql.gotmpl",
		ProjectFields: []string{"timestamp", "container_name", "log"},
	}
	ACMLogsQuery = QueryDefinition{
		Name:          "acmLogs",
		Table:         ContainerLogsTable,
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/acm_logs.kql.gotmpl",
		ProjectFields: []string{"timestamp", "container_name", "log"},
	}
	HostedControlPlaneQuery = QueryDefinition{
		Name:          "hostedControlPlane",
		Table:         ContainerLogsTable,
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/hosted_controlplane.kql.gotmpl",
		ProjectFields: []string{"timestamp", "log"},
	}
	DetailedServiceLogsQuery = QueryDefinition{
		Name:          "detailedServiceLogs",
		Table:         ContainerLogsTable,
		Database:      ServicesDatabase,
		TemplatePath:  "templates/components/detailed_service_logs.kql.gotmpl",
		ProjectFields: []string{"timestamp", "container_name", "msg", "log"},
	}
)

// Query definitions for infra, kubernetes-events, services, hcp, and discovery queries.
var (
	InfraKubernetesEventsQuery = QueryDefinition{
		Name:         "kubernetesEvents",
		Table:        "kubernetesEvents",
		Database:     ServicesDatabase,
		TemplatePath: "templates/infra/kubernetes_events.kql.gotmpl",
	}
	InfraSystemdLogsQuery = QueryDefinition{
		Name:         "systemdLogs",
		Table:        "systemdLogs",
		Database:     ServicesDatabase,
		TemplatePath: "templates/infra/systemd_logs.kql.gotmpl",
	}
	InfraServiceLogsQuery = QueryDefinition{
		Name:         "", // set per-table in loop
		Database:     ServicesDatabase,
		TemplatePath: "templates/infra/service_logs.kql.gotmpl",
	}
	KubernetesEventsSvcQuery = QueryDefinition{
		Name:         "kubernetesEvents",
		Table:        "kubernetesEvents",
		Database:     ServicesDatabase,
		TemplatePath: "templates/kubernetes-events/svc.kql.gotmpl",
	}
	KubernetesEventsMgmtQuery = QueryDefinition{
		Name:         "kubernetesEvents",
		Table:        "kubernetesEvents",
		Database:     ServicesDatabase,
		TemplatePath: "templates/kubernetes-events/mgmt.kql.gotmpl",
	}
	ServiceLogsQueryDef = QueryDefinition{
		Name:         "", // set per-table in loop
		Database:     ServicesDatabase,
		TemplatePath: "templates/services/service_logs.kql.gotmpl",
	}
	HostedControlPlaneLogsQuery = QueryDefinition{
		Name:         "hostedControlPlaneLogs",
		Table:        ContainerLogsTable,
		Database:     HostedControlPlaneLogsDatabase,
		TemplatePath: "templates/hcp/hcp_logs.kql.gotmpl",
	}
	ClusterIdQueryDef = QueryDefinition{
		Name:         "Cluster ID",
		Table:        ClustersServiceLogsTable,
		Database:     ServicesDatabase,
		TemplatePath: "templates/discovery/cluster_id.kql.gotmpl",
	}
	ClusterNamesQueryDef = QueryDefinition{
		Name:         "Cluster Names",
		Table:        ContainerLogsTable,
		TemplatePath: "templates/discovery/cluster_names.kql.gotmpl",
	}
)

// AllQueryDefinitions enumerates every registered QueryDefinition.
// Used by tests to verify template paths and detect dangling templates.
var AllQueryDefinitions = []QueryDefinition{
	DebugQueriesQuery,
	BackendLogsQuery,
	BackendControllerConditionsQuery,
	FrontendLogsQuery,
	ClustersServiceLogsQuery,
	ClustersServicePhasesQuery,
	MaestroLogsQuery,
	HypershiftLogsQuery,
	ACMLogsQuery,
	HostedControlPlaneQuery,
	DetailedServiceLogsQuery,
	InfraKubernetesEventsQuery,
	InfraSystemdLogsQuery,
	InfraServiceLogsQuery,
	KubernetesEventsSvcQuery,
	KubernetesEventsMgmtQuery,
	ServiceLogsQueryDef,
	HostedControlPlaneLogsQuery,
	ClusterIdQueryDef,
	ClusterNamesQueryDef,
}
