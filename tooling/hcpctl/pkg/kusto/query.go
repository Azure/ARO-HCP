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

// kqlEscStr escapes a string as a KQL string literal, wrapping it in single
// quotes and doubling any internal single quotes.
func kqlEscStr(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// kqlEscStrList escapes each element of a string slice as a KQL string literal
// and joins them with commas, suitable for use in has_any() or similar operators.
func kqlEscStrList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = kqlEscStr(item)
	}
	return strings.Join(quoted, ", ")
}

// TemplateData contains all values needed to render a KQL query template.
// String values are pre-escaped as KQL literals (quoted with single quotes).
type TemplateData struct {
	Table              string    `kqlParameter:"table"`
	NoTruncation       bool      `kqlParameter:"noTruncation"`
	Limit              int       `kqlParameter:"limit"`
	TimestampMin       time.Time `kqlParameter:"timestampMin"`
	TimestampMax       time.Time `kqlParameter:"timestampMax"`
	ClusterName        string    `kqlParameter:"clusterName"`
	ClusterNames       string    `kqlParameter:"clusterNames"`
	SubResourceGroupId string    `kqlParameter:"subResourceGroupId"`
	ResourceGroupName  string    `kqlParameter:"resourceGroupName"`
	ClusterId          string    `kqlParameter:"clusterId"`
	ClusterIds         string    `kqlParameter:"clusterIds"`
	HCPNamespacePrefix string    `kqlParameter:"hcpNamespacePrefix"`
	ProjectFields      string    `kqlParameter:"projectFields"`
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

func WithClusterNames(clusterNames []string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterNames = kqlEscStrList(clusterNames)
	}
}

func NewTemplateDataFromOptions(queryOptions QueryOptions, options ...TemplateDataOptions) TemplateData {
	templateData := TemplateData{
		NoTruncation:       queryOptions.Limit < 0,
		Limit:              max(queryOptions.Limit, 0),
		TimestampMin:       queryOptions.TimestampMin,
		TimestampMax:       queryOptions.TimestampMax,
		SubResourceGroupId: kqlEscStr(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", queryOptions.SubscriptionId, queryOptions.ResourceGroupName)),
		ResourceGroupName:  kqlEscStr(queryOptions.ResourceGroupName),
	}
	defaults := []TemplateDataOptions{
		WithClusterName(queryOptions.InfraClusterName),
		WithClusterNames([]string{queryOptions.InfraClusterName}),
	}
	for _, option := range append(defaults, options...) {
		option(&templateData)
	}
	return templateData
}

type TemplatingMode bool

// QueryFactory creates Query instances from templates, binding parameters at creation time.
type QueryFactory struct {
	BuiltinQueryDefinitions []QueryDefinition
	CustomQueryDefinitions  []QueryDefinition
}

// NewQueryFactory creates a factory with all the shared query parameters.
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
		BuiltinQueryDefinitions: builtinQueryDefinitions,
		CustomQueryDefinitions:  customQueryDefinitions,
	}, nil
}

// GetBuiltinQueryDefinition returns the builtin query definition with the given name.
func (f *QueryFactory) GetBuiltinQueryDefinition(name string) (*QueryDefinition, error) {
	for _, def := range f.BuiltinQueryDefinitions {
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

// buildQuery builds a query from a template, binding parameters at creation time.
// If unsafeTemplating is true, the Parameters are used as is, otherwise they are converted to KQL parameters to avoid injection.
func (f *QueryFactory) buildQuery(name, database, templateName string, queryType QueryType, data TemplateData, unlimited bool) (*templateQuery, error) {
	templateString := GetTemplate(templateName)

	builder := kql.New("")
	var buf bytes.Buffer

	funcMap := template.FuncMap{
		"kqlDatetime": func(t time.Time) string {
			return "datetime(" + t.UTC().Format("2006-01-02T15:04:05.0000000Z") + ")"
		},
	}

	tmplRegular, err := template.New("query-template").Funcs(funcMap).Parse(templateString)
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

// BuildCustomQuery builds a single custom query by name, returning the built queries.
func (f *QueryFactory) BuildCustomQuery(name string, templateData TemplateData) ([]Query, error) {
	for _, def := range f.CustomQueryDefinitions {
		if def.Name == name {
			return f.Build(def, templateData)
		}
	}
	return nil, fmt.Errorf("custom query %q not found", name)
}

// BuildAllCustomQueries loads all custom queries, returning a flat slice of built queries.
func (f *QueryFactory) BuildAllCustomQueries(templateData TemplateData) ([]Query, error) {
	var queries []Query
	for _, def := range f.CustomQueryDefinitions {
		qs, err := f.Build(def, templateData)
		if err != nil {
			return nil, fmt.Errorf("failed to build custom query %q: %w", def.Name, err)
		}
		queries = append(queries, qs...)
	}
	return queries, nil
}

// BuildMergedCustomQuery builds a single custom query by name and merges all its
// templates into a single Query.
func (f *QueryFactory) BuildMergedCustomQuery(name string, templateData TemplateData) (Query, error) {
	for _, def := range f.CustomQueryDefinitions {
		if def.Name == name {
			return f.BuildMerged(def, templateData)
		}
	}
	return nil, fmt.Errorf("custom query %q not found", name)
}

// BuildAllMergedCustomQueries builds all custom queries, merging each definition's templates
// into a single Query per definition.
func (f *QueryFactory) BuildAllMergedCustomQueries(templateData TemplateData) ([]Query, error) {
	var queries []Query
	for _, def := range f.CustomQueryDefinitions {
		q, err := f.BuildMerged(def, templateData)
		if err != nil {
			return nil, fmt.Errorf("failed to build custom query %q: %w", def.Name, err)
		}
		queries = append(queries, q)
	}
	return queries, nil
}
