package kusto

// QueryDefinition declaratively describes a group of related KQL queries.
// Each template path maps to a single KQL query.
type QueryDefinition struct {
	Name          string   `yaml:"name"`
	Database      string   `yaml:"database"`
	TemplatePaths []string `yaml:"templatePaths"`
}

var ServicesDatabase = "ServiceLogs"
var HostedControlPlaneLogsDatabase = "HostedControlPlaneLogs"

var (
	InfraKubernetesEventsQuery = QueryDefinition{
		Name:          "kubernetesEvents",
		Database:      ServicesDatabase,
		TemplatePaths: []string{"templates/infra/kubernetes_events.kql.gotmpl"},
	}
	InfraSystemdLogsQuery = QueryDefinition{
		Name:          "systemdLogs",
		Database:      ServicesDatabase,
		TemplatePaths: []string{"templates/infra/systemd_logs.kql.gotmpl"},
	}
	InfraServiceLogsQuery = QueryDefinition{
		Name:          "serviceLogs",
		Database:      ServicesDatabase,
		TemplatePaths: []string{"templates/infra/service_logs.kql.gotmpl"},
	}
	KubernetesEventsSvcQuery = QueryDefinition{
		Name:          "kubernetesEventsSvc",
		Database:      ServicesDatabase,
		TemplatePaths: []string{"templates/kubernetes-events/svc.kql.gotmpl"},
	}
	KubernetesEventsMgmtQuery = QueryDefinition{
		Name:          "kubernetesEventsMgmt",
		Database:      ServicesDatabase,
		TemplatePaths: []string{"templates/kubernetes-events/mgmt.kql.gotmpl"},
	}
	ServiceLogsQueryDef = QueryDefinition{
		Name:          "serviceLogs",
		Database:      ServicesDatabase,
		TemplatePaths: []string{"templates/services/service_logs.kql.gotmpl"},
	}
	HostedControlPlaneLogsQuery = QueryDefinition{
		Name:          "hostedControlPlaneLogs",
		Database:      HostedControlPlaneLogsDatabase,
		TemplatePaths: []string{"templates/hcp/hcp_logs.kql.gotmpl"},
	}
	ClusterIdQueryDef = QueryDefinition{
		Name:          "clusterId",
		Database:      ServicesDatabase,
		TemplatePaths: []string{"templates/discovery/cluster_id.kql.gotmpl"},
	}
	ClusterNamesQueryDef = QueryDefinition{
		Name:          "clusterNames",
		Database:      ServicesDatabase,
		TemplatePaths: []string{"templates/discovery/cluster_names.kql.gotmpl"},
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
