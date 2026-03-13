package kusto

// QueryDefinition declaratively describes a single KQL query.
type QueryDefinition struct {
	Name          string
	Database      string
	TemplatePath  string
	ProjectFields []string // nil = no project field substitution
}

var ServicesDatabase = "ServiceLogs"
var HostedControlPlaneLogsDatabase = "HostedControlPlaneLogs"

var (
	InfraKubernetesEventsQuery = QueryDefinition{
		Name:         "kubernetesEvents",
		Database:     ServicesDatabase,
		TemplatePath: "templates/infra/kubernetes_events.kql.gotmpl",
	}
	InfraSystemdLogsQuery = QueryDefinition{
		Name:         "systemdLogs",
		Database:     ServicesDatabase,
		TemplatePath: "templates/infra/systemd_logs.kql.gotmpl",
	}
	InfraServiceLogsQuery = QueryDefinition{
		Name:         "serviceLogs",
		Database:     ServicesDatabase,
		TemplatePath: "templates/infra/service_logs.kql.gotmpl",
	}
	KubernetesEventsSvcQuery = QueryDefinition{
		Name:         "kubernetesEvents",
		Database:     ServicesDatabase,
		TemplatePath: "templates/kubernetes-events/svc.kql.gotmpl",
	}
	KubernetesEventsMgmtQuery = QueryDefinition{
		Name:         "kubernetesEvents",
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
		Database:     HostedControlPlaneLogsDatabase,
		TemplatePath: "templates/hcp/hcp_logs.kql.gotmpl",
	}
	ClusterIdQueryDef = QueryDefinition{
		Name:         "Cluster ID",
		Database:     ServicesDatabase,
		TemplatePath: "templates/discovery/cluster_id.kql.gotmpl",
	}
	ClusterNamesQueryDef = QueryDefinition{
		Name:         "Cluster Names",
		Database:     ServicesDatabase,
		TemplatePath: "templates/discovery/cluster_names.kql.gotmpl",
	}
)

var AllQueryDefinitions = []QueryDefinition{
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
