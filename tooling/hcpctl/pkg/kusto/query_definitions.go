package kusto

// QueryDefinition declaratively describes a group of related KQL queries.
// For single-template queries, set TemplatePath directly.
// For multi-template queries, use Children with specific names and template paths.
type QueryDefinition struct {
	Name         string       `yaml:"name"`
	QueryType    QueryType    `yaml:"queryType"`
	Database     string       `yaml:"database"`
	TemplatePath string       `yaml:"templatePath,omitempty"`
	Children     []QueryChild `yaml:"children,omitempty"`
}

// QueryChild describes a named sub-query within a multi-template QueryDefinition.
type QueryChild struct {
	Name         string `yaml:"name"`
	TemplatePath string `yaml:"templatePath"`
}

type QueryType string

const (
	QueryTypeServices           QueryType = "services"
	QueryTypeHostedControlPlane QueryType = "hosted-control-plane"
	QueryTypeInternal           QueryType = "must-gather-internal"
	QueryTypeKubernetesEvents   QueryType = "kubernetes-events"
	QueryTypeSystemdLogs        QueryType = "systemd-logs"
	QueryTypeCustomLogs         QueryType = "custom-logs"
)
