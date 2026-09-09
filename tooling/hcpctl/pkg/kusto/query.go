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
	"strings"
	"text/template"
	"time"

	"github.com/Azure/azure-kusto-go/azkustodata/kql"
)

type OrderBy int

const (
	OrderByAsc OrderBy = iota
	OrderByDesc
)

func (o OrderBy) String() string {
	switch o {
	case OrderByDesc:
		return "desc"
	default:
		return "asc"
	}
}

// QueryOptions contains the parameters needed to construct queries.
type QueryOptions struct {
	SubscriptionId    string
	ResourceGroupName string
	InfraClusterName  string
	ClusterIds        []string
	ClusterNames      []string
	TimestampMin      time.Time
	TimestampMax      time.Time
	Limit             int
	SplitByPod        bool
	OrderBy           OrderBy
}

func NewQueryOptions() QueryOptions {
	return QueryOptions{
		OrderBy: OrderByAsc,
	}
}

// Query represents a ready-to-execute KQL query with all its metadata.
type Query interface {
	GetName() string
	GetQueryType() QueryType
	GetDatabase() string
	GetQuery() *kql.Builder
	IsUnlimited() bool
}

// TimeRangeQuery is a query that can be safely divided into independently
// ordered timestamp ranges.
type TimeRangeQuery interface {
	Query
	IsPageable() bool
	GetTimeRange() (time.Time, time.Time)
	GetOrderBy() OrderBy
	WithTimeRange(timestampMin, timestampMax time.Time) (Query, error)
}

func (q *templateQuery) IsPageable() bool {
	return q.pageable
}

// templateQuery is a Query backed by a rendered Go text/template.
type templateQuery struct {
	name         string
	queryType    QueryType
	database     string
	query        *kql.Builder
	unlimited    bool
	templateData TemplateData
	pageable     bool
}

func (q *templateQuery) GetName() string {
	return q.name
}

func (q *templateQuery) GetQueryType() QueryType {
	return q.queryType
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

func (q *templateQuery) GetTimeRange() (time.Time, time.Time) {
	return q.templateData.timestampMin, q.templateData.timestampMax
}

func (q *templateQuery) GetOrderBy() OrderBy {
	return q.templateData.orderBy
}

func (q *templateQuery) WithTimeRange(timestampMin, timestampMax time.Time) (Query, error) {
	if !q.pageable {
		return nil, fmt.Errorf("query %q cannot be divided into timestamp ranges", q.name)
	}

	queryWithoutSort, orderBy, err := removeFinalOrderBy(q.query.String())
	if err != nil {
		return nil, fmt.Errorf("query %q cannot be divided into timestamp ranges: %w", q.name, err)
	}
	queryWithoutSort = strings.TrimPrefix(queryWithoutSort, "set notruncation;\n")

	builder := kql.New("")
	builder.AddUnsafe(fmt.Sprintf(
		"%s\n| where timestamp between (%s .. %s)\n%s",
		strings.TrimSpace(queryWithoutSort),
		kqlDatetime(timestampMin),
		kqlDatetime(timestampMax),
		orderBy,
	))

	pageData := q.templateData
	pageData.timestampMin = timestampMin
	pageData.timestampMax = timestampMax
	return &templateQuery{
		name:         q.name,
		queryType:    q.queryType,
		database:     q.database,
		query:        builder,
		templateData: pageData,
	}, nil
}

func removeFinalOrderBy(query string) (string, string, error) {
	orderByStart := strings.LastIndex(query, "| order by ")
	if orderByStart < 0 {
		return "", "", fmt.Errorf("final order-by clause not found")
	}
	orderByEnd := strings.IndexByte(query[orderByStart:], '\n')
	if orderByEnd < 0 {
		return query[:orderByStart], query[orderByStart:], nil
	}
	orderByEnd += orderByStart
	return query[:orderByStart] + query[orderByEnd+1:], query[orderByStart:orderByEnd], nil
}

func (q *templateQuery) String() string {
	return q.query.String()
}

// kqlEscStr escapes a single string as a KQL string literal to avoid KQL injection
func kqlEscStr(str string) string {
	return strings.ReplaceAll(str, "'", "''")
}

// kqlEscStrList escapes each element of a string slice as a KQL string literal
// and joins them with commas, suitable for use in has_any() or similar operators.
func kqlEscStrList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = "'" + kqlEscStr(item) + "'"
	}
	return strings.Join(quoted, ", ")
}

// kqlDatetime converts a time.Time to a KQL datetime string.
func kqlDatetime(t time.Time) string {
	return "datetime(" + t.UTC().Format("2006-01-02T15:04:05.0000000Z") + ")"
}

// TemplateData contains all values needed to render a KQL query template.
// String values are pre-escaped as KQL literals (quoted with single quotes).
type TemplateData struct {
	Table              string
	NoTruncation       bool
	Limit              int
	TimestampMin       string
	TimestampMax       string
	ClusterName        string
	ClusterNames       string
	SubResourceGroupId string
	ResourceGroupName  string
	ClusterId          string
	ClusterIds         string
	FilterClusterName  string
	HCPNamespacePrefix string
	// Namespace is the Kubernetes namespace to scope a query to (e.g. an
	// oc-adm-inspect resource/event/log query). Single quotes are escaped for safe
	// embedding inside a single-quoted KQL string literal; the template supplies
	// the surrounding quotes (e.g. '{{.Namespace}}').
	Namespace    string
	SplitByPod   bool
	OrderBy      string
	timestampMin time.Time
	timestampMax time.Time
	orderBy      OrderBy
}

type TemplateDataOptions func(*TemplateData)

func WithTable(table string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.Table = table
	}
}

func WithClusterId(clusterId string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterId = kqlEscStr(clusterId)
	}
}

func WithClusterIds(clusterIds []string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterIds = kqlEscStrList(clusterIds)
	}
}

func WithFilterClusterName(name string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.FilterClusterName = kqlEscStr(name)
	}
}

func WithHCPNamespacePrefix(hcpNamespacePrefix string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.HCPNamespacePrefix = kqlEscStr(hcpNamespacePrefix)
	}
}

func WithClusterName(clusterName string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterName = kqlEscStr(clusterName)
	}
}

func WithNamespace(namespace string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.Namespace = kqlEscStr(namespace)
	}
}

func WithClusterNames(clusterNames []string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterNames = kqlEscStrList(clusterNames)
	}
}

func WithTimestampMin(timestampMin time.Time) TemplateDataOptions {
	return func(d *TemplateData) {
		d.TimestampMin = kqlDatetime(timestampMin)
	}
}

func WithTimestampMax(timestampMax time.Time) TemplateDataOptions {
	return func(d *TemplateData) {
		d.TimestampMax = kqlDatetime(timestampMax)
	}
}

func NewTemplateDataFromOptions(queryOptions QueryOptions, options ...TemplateDataOptions) TemplateData {
	templateData := TemplateData{
		NoTruncation:       queryOptions.Limit < 0,
		Limit:              max(queryOptions.Limit, 0),
		SubResourceGroupId: fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", kqlEscStr(queryOptions.SubscriptionId), kqlEscStr(queryOptions.ResourceGroupName)),
		ResourceGroupName:  kqlEscStr(queryOptions.ResourceGroupName),
		SplitByPod:         queryOptions.SplitByPod,
		OrderBy:            queryOptions.OrderBy.String(),
		timestampMin:       queryOptions.TimestampMin,
		timestampMax:       queryOptions.TimestampMax,
		orderBy:            queryOptions.OrderBy,
	}
	defaults := []TemplateDataOptions{
		WithClusterName(queryOptions.InfraClusterName),
		WithClusterNames([]string{queryOptions.InfraClusterName}),
		WithTimestampMin(queryOptions.TimestampMin),
		WithTimestampMax(queryOptions.TimestampMax),
	}
	for _, option := range append(defaults, options...) {
		option(&templateData)
	}
	return templateData
}

type TemplatingMode bool

// QueryFactory creates Query instances from templates, binding parameters at creation time.
type QueryFactory struct {
	builtinQueryDefinitions []QueryDefinition
	customQueryDefinitions  []QueryDefinition
}

// NewQueryFactory creates a go templatized KQL query factory.
func NewQueryFactory() (*QueryFactory, error) {
	builtinQueryDefinitions, err := LoadBuiltinQueryDefinitions()
	if err != nil {
		return nil, fmt.Errorf("failed to load builtin query definitions: %w", err)
	}
	customQueryDefinitions, err := LoadCustomQueryDefinitions()
	if err != nil {
		return nil, fmt.Errorf("failed to load custom query definitions: %w", err)
	}
	return &QueryFactory{
		builtinQueryDefinitions: builtinQueryDefinitions,
		customQueryDefinitions:  customQueryDefinitions,
	}, nil
}

func (f *QueryFactory) buildQuery(name, database, templateName string, queryType QueryType, data TemplateData, unlimited bool) (*templateQuery, error) {
	templateString := GetTemplate(templateName)
	return renderTemplateQuery(name, database, queryType, templateString, data, unlimited)
}

func renderTemplateQuery(name, database string, queryType QueryType, templateString string, data TemplateData, unlimited bool) (*templateQuery, error) {
	builder := kql.New("")
	var buf bytes.Buffer

	tmplRegular, err := template.New("query-template").Parse(templateString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse query template %q: %w", name, err)
	}
	err = tmplRegular.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("failed to render query template %q: %w", name, err)
	}

	rendered := buf.String()
	builder.AddUnsafe(rendered)

	return &templateQuery{
		name:         name,
		database:     database,
		queryType:    queryType,
		query:        builder,
		unlimited:    unlimited,
		templateData: data,
		pageable:     isPageableQuery(queryType, rendered),
	}, nil
}

func isPageableQuery(queryType QueryType, rendered string) bool {
	switch queryType {
	case QueryTypeServices, QueryTypeHostedControlPlane, QueryTypeKubernetesEvents, QueryTypeSystemdLogs,
		QueryTypeOCAdmInspectEvents, QueryTypeOCAdmInspectLogs:
		return true
	case QueryTypeCustomLogs:
		return strings.Contains(rendered, "| order by timestamp")
	default:
		return false
	}
}

// GetAllCustomQueryDefinitions returns all custom query definitions.
func (f *QueryFactory) GetAllCustomQueryDefinitions() []QueryDefinition {
	return f.customQueryDefinitions
}

// GetCustomQueryDefinition returns the custom query definition with the given name.
func (f *QueryFactory) GetCustomQueryDefinition(name string) (*QueryDefinition, error) {
	for _, def := range f.customQueryDefinitions {
		if def.Name == name {
			return &def, nil
		}
	}
	return nil, fmt.Errorf("custom query %q not found", name)
}

// GetBuiltinQueryDefinition returns the builtin query definition with the given name.
func (f *QueryFactory) GetBuiltinQueryDefinition(name string) (*QueryDefinition, error) {
	for _, def := range f.builtinQueryDefinitions {
		if def.Name == name {
			return &def, nil
		}
	}
	return nil, fmt.Errorf("builtin query %q not found", name)
}

// Build constructs Queries from a QueryDefinition, applying template data and project fields.
// For single-template definitions (TemplatePath set), it produces one Query.
// For multi-template definitions (Children set), each child produces one Query with its own name.
func (f *QueryFactory) Build(def QueryDefinition, templateData TemplateData) ([]Query, error) {
	if len(def.Children) > 0 {
		var queries []Query
		for _, child := range def.Children {
			q, err := f.buildQuery(child.Name, def.Database, child.TemplatePath, def.QueryType, templateData, templateData.NoTruncation)
			if err != nil {
				return nil, err
			}
			queries = append(queries, q)
		}
		return queries, nil
	}
	q, err := f.buildQuery(def.Name, def.Database, def.TemplatePath, def.QueryType, templateData, templateData.NoTruncation)
	if err != nil {
		return nil, err
	}
	return []Query{q}, nil
}

// BuildMerged constructs a single Query from a QueryDefinition by rendering all templates
// and joining them with newlines. This is useful for generating a single Kusto deep-link
// that contains multiple query statements.
func (f *QueryFactory) BuildMerged(def QueryDefinition, templateData TemplateData) (Query, error) {
	queries, err := f.Build(def, templateData)
	if err != nil {
		return nil, err
	}
	if len(queries) == 1 {
		return queries[0], nil
	}
	var parts []string
	for _, q := range queries {
		parts = append(parts, q.GetQuery().String())
	}
	merged := kql.New("")
	merged.AddUnsafe(strings.Join(parts, "\n\n"))
	return &templateQuery{
		name:      def.Name,
		database:  def.Database,
		query:     merged,
		unlimited: queries[0].IsUnlimited(),
	}, nil
}
