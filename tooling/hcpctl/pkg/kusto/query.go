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

// QueryOptions contains the parameters needed to construct queries.
type QueryOptions struct {
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
	GetQueryType() QueryType
	GetDatabase() string
	GetQuery() *kql.Builder
	IsUnlimited() bool
}

// templateQuery is a Query backed by a rendered Go text/template.
type templateQuery struct {
	name      string
	queryType QueryType
	database  string
	query     *kql.Builder
	unlimited bool
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

func (q *templateQuery) String() string {
	return q.query.String()
}

// kqlEscStrList escapes each element of a string slice as a KQL string literal
// and joins them with commas, suitable for use in has_any() or similar operators.
func kqlEscStrList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = "'" + strings.ReplaceAll(item, "'", "''") + "'"
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
	HCPNamespacePrefix string
}

type TemplateDataOptions func(*TemplateData)

func WithTable(table string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.Table = table
	}
}

func WithClusterId(clusterId string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterId = clusterId
	}
}

func WithClusterIds(clusterIds []string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterIds = kqlEscStrList(clusterIds)
	}
}

func WithHCPNamespacePrefix(hcpNamespacePrefix string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.HCPNamespacePrefix = hcpNamespacePrefix
	}
}

func WithClusterName(clusterName string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterName = clusterName
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
		SubResourceGroupId: fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", queryOptions.SubscriptionId, queryOptions.ResourceGroupName),
		ResourceGroupName:  queryOptions.ResourceGroupName,
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

	builder := kql.New("")
	var buf bytes.Buffer

	tmplRegular, err := template.New("query-template").Parse(templateString)
	if err != nil {
		return nil, fmt.Errorf("failed to render template %q: %w", templateName, err)
	}
	err = tmplRegular.Execute(&buf, data)
	if err != nil {
		return nil, fmt.Errorf("failed to render template %q: %w", templateName, err)
	}

	builder.AddUnsafe(buf.String())

	return &templateQuery{
		name:      name,
		database:  database,
		queryType: queryType,
		query:     builder,
		unlimited: unlimited,
	}, nil
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
