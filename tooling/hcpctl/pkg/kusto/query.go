// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kusto

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"text/template"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata/kql"
)

var ServicesDatabase = "ServiceLogs"
var HostedControlPlaneLogsDatabase = "HostedControlPlaneLogs"

var ServicesTables = []string{
	"containerLogs",
	"clustersServiceLogs",
	"frontendLogs",
	"backendLogs",
}

var HCPNamespacePrefix = "ocm-arohcp"

var ContainerLogsTable = ServicesTables[0]
var ClustersServiceLogsTable = ServicesTables[1]

// QueryOptions contains the parameters needed to construct queries.
type QueryOptions struct {
	ClusterIds        []string
	SubscriptionId    string
	ResourceGroupName string
	InfraClusterName  string
	TimestampMin      time.Time
	TimestampMax      time.Time
	Limit             int
}

// Query represents a ready-to-execute KQL query with all its metadata.
type Query interface {
	GetName() string
	GetDatabase() string
	GetQuery() *kql.Builder
	GetParameters() *kql.Parameters
	IsUnlimited() bool
}

// templateQuery is a Query backed by a rendered Go text/template.
type templateQuery struct {
	name       string
	database   string
	query      *kql.Builder
	parameters *kql.Parameters
	unlimited  bool
}

func (q *templateQuery) GetName() string {
	return q.name
}

func (q *templateQuery) GetParameters() *kql.Parameters {
	return q.parameters
}

func (q *templateQuery) GetDatabase() string {
	return q.database
}

func (q *templateQuery) GetQuery() *kql.Builder {
	return q.query
}

func (q *templateQuery) IsUnlimited() bool {
	return q.unlimited
}

func (q *templateQuery) String() string {
	return q.query.String()
}

// StandardProjectFields is the standard set of project fields for KQL queries.
var StandardProjectFields = []string{"timestamp", "log", "cluster", "namespace_name", "container_name"}

// TemplateData contains all values needed to render a KQL query template.
// Values are rendered directly into the query string as KQL literals.
type TemplateData struct {
	Table              string    `kqlParameter:"table"`
	NoTruncation       bool      `kqlParameter:"noTruncation"`
	Limit              int       `kqlParameter:"limit"`
	HasClusterIds      bool      `kqlParameter:"hasClusterIds"`
	TimestampMin       time.Time `kqlParameter:"timestampMin"`
	TimestampMax       time.Time `kqlParameter:"timestampMax"`
	ClusterName        string    `kqlParameter:"clusterName"`
	SubResourceGroupId string    `kqlParameter:"subResourceGroupId"`
	ResourceGroupName  string    `kqlParameter:"resourceGroupName"`
	ClusterId          string    `kqlParameter:"clusterId"`
	ClusterIds         string    `kqlParameter:"clusterIds"`
	HCPNamespacePrefix string    `kqlParameter:"hcpNamespacePrefix"`
	ProjectFields      string    `kqlParameter:"projectFields"`
}

func (d *TemplateData) PreprocessParameterBindings(useTags bool) map[string]any {
	val := reflect.ValueOf(d)

	// If a pointer is passed, get the underlying value
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}

	typ := val.Type()
	result := make(map[string]any)

	timeType := reflect.TypeOf(time.Time{})

	// Iterate over all fields in the struct
	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		fieldName := field.Name

		if useTags {
			// Extract the value from the `kqlParameter` tag
			tagValue := field.Tag.Get("kqlParameter")
			result[fieldName] = tagValue
		} else {
			// Extract the actual runtime value of the field
			fieldValue := val.Field(i).Interface()
			// Format time.Time values as KQL datetime literals
			if field.Type == timeType {
				t := fieldValue.(time.Time)
				result[fieldName] = fmt.Sprintf("datetime(\"%s\")", t.UTC().Format(time.RFC3339Nano))
			} else {
				result[fieldName] = fieldValue
			}
		}
	}

	return result
}

func (d *TemplateData) CreateKQLParameters() *kql.Parameters {
	kqlParameters := kql.NewParameters()
	kqlParameters.AddString("table", d.Table)
	kqlParameters.AddBool("noTruncation", d.NoTruncation)
	kqlParameters.AddInt("limit", int32(d.Limit))
	kqlParameters.AddBool("hasClusterIds", d.HasClusterIds)
	kqlParameters.AddDateTime("timestampMin", d.TimestampMin)
	kqlParameters.AddDateTime("timestampMax", d.TimestampMax)
	kqlParameters.AddString("clusterName", d.ClusterName)
	kqlParameters.AddString("subResourceGroupId", d.SubResourceGroupId)
	kqlParameters.AddString("resourceGroupName", d.ResourceGroupName)
	kqlParameters.AddString("clusterId", d.ClusterId)
	return kqlParameters
}

// QueryFactory creates Query instances from templates, binding parameters at creation time.
type QueryFactory struct {
	queryOptions         *QueryOptions
	UnsafeTemplating     bool
	MergeStandardProject bool
}

// NewQueryFactory creates a factory with all the shared query parameters.
func NewQueryFactory(opts *QueryOptions, unsafeTemplating bool) *QueryFactory {
	return &QueryFactory{
		queryOptions:     opts,
		UnsafeTemplating: unsafeTemplating,
	}
}

// Build constructs a Query from a QueryDefinition, applying template data and project fields.
func (f *QueryFactory) Build(def QueryDefinition) (Query, error) {
	data := f.templateData(def.Table)
	if def.ProjectFields != nil {
		data.ProjectFields = f.projectFields(def.ProjectFields)
	}
	return f.buildQuery(def.Name, def.Database, def.TemplatePath, data, f.queryOptions.Limit < 0)
}

// buildForTables builds one query per table using the given definition as a base,
// overriding Name and Table for each entry.
func (f *QueryFactory) buildForTables(tables []string, base QueryDefinition) ([]Query, error) {
	queries := make([]Query, 0, len(tables))
	for _, table := range tables {
		def := base
		def.Name = table
		def.Table = table
		q, err := f.Build(def)
		if err != nil {
			return nil, err
		}
		queries = append(queries, q)
	}
	return queries, nil
}

func (f *QueryFactory) SetClusterIds(clusterIds []string) {
	f.queryOptions.ClusterIds = clusterIds
}

func (f *QueryFactory) SetInfraClusterName(name string) {
	f.queryOptions.InfraClusterName = name
}

func (f *QueryFactory) templateData(table string) TemplateData {
	return TemplateData{
		Table:              table,
		NoTruncation:       f.queryOptions.Limit < 0,
		Limit:              max(f.queryOptions.Limit, 0),
		HasClusterIds:      len(f.queryOptions.ClusterIds) != 0,
		TimestampMin:       f.queryOptions.TimestampMin,
		TimestampMax:       f.queryOptions.TimestampMax,
		ClusterName:        f.queryOptions.InfraClusterName,
		SubResourceGroupId: f.subResourceGroupId(),
		ResourceGroupName:  f.queryOptions.ResourceGroupName,
		HCPNamespacePrefix: HCPNamespacePrefix,
		ClusterIds:         formatKQLStringArray(f.queryOptions.ClusterIds),
	}
}

func (f *QueryFactory) projectFields(baseFields []string) string {
	return mergeProjectFields(baseFields, f.MergeStandardProject)
}

func mergeProjectFields(baseFields []string, mergeStandard bool) string {
	if !mergeStandard {
		return strings.Join(baseFields, ", ")
	}
	merged := make([]string, len(StandardProjectFields))
	copy(merged, StandardProjectFields)
	seen := make(map[string]bool)
	for _, f := range StandardProjectFields {
		seen[f] = true
	}
	for _, f := range baseFields {
		if !seen[f] {
			merged = append(merged, f)
		}
	}
	return strings.Join(merged, ", ")
}

func (f *QueryFactory) subResourceGroupId() string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", f.queryOptions.SubscriptionId, f.queryOptions.ResourceGroupName)
}

func formatKQLStringArray(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + v + "'"
	}
	return strings.Join(quoted, ", ")
}

// buildQuery builds a query from a template, binding parameters at creation time.
// If unsafeTemplating is true, the Parameters are used as is, otherwise they are converted to KQL parameters to avoid injection.
func (f *QueryFactory) buildQuery(name, database, templateName string, data TemplateData, unlimited bool) (*templateQuery, error) {
	templateString := GetTemplate(templateName)

	parameters := data.PreprocessParameterBindings(!f.UnsafeTemplating)

	var bufParameters bytes.Buffer
	tmplParameters, err := template.New("query").Delims("<<", ">>").Parse(templateString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template %q: %w", templateName, err)
	}
	err = tmplParameters.Execute(&bufParameters, parameters)
	if err != nil {
		return nil, fmt.Errorf("failed to render template parameters %q: %w", templateName, err)
	}

	parsedWithParameters := bufParameters.String()

	var bufRegular bytes.Buffer
	tmplRegular, err := template.New("parse-regular").Parse(parsedWithParameters)
	if err != nil {
		return nil, fmt.Errorf("failed to render template %q: %w", templateName, err)
	}
	err = tmplRegular.Execute(&bufRegular, data)
	if err != nil {
		return nil, fmt.Errorf("failed to render template %q: %w", templateName, err)
	}

	builder := kql.New("")
	builder.AddUnsafe(bufRegular.String())

	var parametersKQL *kql.Parameters
	if !f.UnsafeTemplating {
		parametersKQL = data.CreateKQLParameters()
	}

	return &templateQuery{
		name:       name,
		database:   database,
		query:      builder,
		parameters: parametersKQL,
		unlimited:  unlimited,
	}, nil
}

func (f *QueryFactory) InfraKubernetesEvents() ([]Query, error) {
	q, err := f.Build(InfraKubernetesEventsQuery)
	if err != nil {
		return nil, err
	}
	return []Query{q}, nil
}

func (f *QueryFactory) KubernetesEventsSvc() ([]Query, error) {
	q, err := f.Build(KubernetesEventsSvcQuery)
	if err != nil {
		return nil, err
	}
	return []Query{q}, nil
}

func (f *QueryFactory) KubernetesEventsMgmt() ([]Query, error) {
	if len(f.queryOptions.ClusterIds) == 0 {
		return nil, nil
	}
	q, err := f.Build(KubernetesEventsMgmtQuery)
	if err != nil {
		return nil, err
	}
	return []Query{q}, nil
}

func (f *QueryFactory) InfraSystemdLogs() ([]Query, error) {
	q, err := f.Build(InfraSystemdLogsQuery)
	if err != nil {
		return nil, err
	}
	return []Query{q}, nil
}

func (f *QueryFactory) InfraServiceLogs() ([]Query, error) {
	return f.buildForTables(ServicesTables, InfraServiceLogsQuery)
}

func (f *QueryFactory) ServiceLogs() ([]Query, error) {
	return f.buildForTables(ServicesTables, ServiceLogsQueryDef)
}

func (f *QueryFactory) HostedControlPlaneLogs() ([]Query, error) {
	queries := make([]Query, 0, len(f.queryOptions.ClusterIds))
	for _, clusterId := range f.queryOptions.ClusterIds {
		def := HostedControlPlaneLogsQuery
		def.Table = ContainerLogsTable
		data := f.templateData(def.Table)
		data.ClusterId = clusterId
		if def.ProjectFields != nil {
			data.ProjectFields = f.projectFields(def.ProjectFields)
		}
		q, err := f.buildQuery(def.Name, def.Database, def.TemplatePath, data, f.queryOptions.Limit < 0)
		if err != nil {
			return nil, err
		}
		queries = append(queries, q)
	}
	return queries, nil
}

func (f *QueryFactory) ClusterIdQuery() (Query, error) {
	return f.Build(ClusterIdQueryDef)
}

func (f *QueryFactory) ClusterNamesQueries() ([]Query, error) {
	databases := []string{ServicesDatabase, HostedControlPlaneLogsDatabase}
	queries := make([]Query, 0, len(databases))
	for _, db := range databases {
		def := ClusterNamesQueryDef
		def.Database = db
		q, err := f.Build(def)
		if err != nil {
			return nil, err
		}
		queries = append(queries, q)
	}
	return queries, nil
}

func (f *QueryFactory) BackendLogs() (Query, error) {
	return f.Build(BackendLogsQuery)
}

func (f *QueryFactory) BackendControllerConditions() (Query, error) {
	return f.Build(BackendControllerConditionsQuery)
}

func (f *QueryFactory) FrontendLogs() (Query, error) {
	return f.Build(FrontendLogsQuery)
}

func (f *QueryFactory) ClustersServiceLogs() (Query, error) {
	return f.Build(ClustersServiceLogsQuery)
}

func (f *QueryFactory) ClustersServicePhases() (Query, error) {
	return f.Build(ClustersServicePhasesQuery)
}

func (f *QueryFactory) MaestroLogs() (Query, error) {
	return f.Build(MaestroLogsQuery)
}

func (f *QueryFactory) HypershiftLogs() (Query, error) {
	return f.Build(HypershiftLogsQuery)
}

func (f *QueryFactory) ACMLogs() (Query, error) {
	return f.Build(ACMLogsQuery)
}

func (f *QueryFactory) HostedControlPlane() (Query, error) {
	return f.Build(HostedControlPlaneQuery)
}

func (f *QueryFactory) DetailedServiceLogs() (Query, error) {
	return f.Build(DetailedServiceLogsQuery)
}

func (f *QueryFactory) DebugQueries() (Query, error) {
	return f.Build(DebugQueriesQuery)
}
