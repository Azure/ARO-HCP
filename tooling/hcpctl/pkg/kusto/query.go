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
	TimestampMin       time.Time `kqlParameter:"timestampMin"`
	TimestampMax       time.Time `kqlParameter:"timestampMax"`
	ClusterName        string    `kqlParameter:"clusterName"`
	ClusterNames       []string  `kqlParameter:"clusterNames"`
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
		d.ClusterId = clusterId
	}
}

func WithClusterIds(clusterIds []string) TemplateDataOptions {
	return func(d *TemplateData) {
		d.ClusterIds = strings.Join(clusterIds, ", ")
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
		d.ClusterNames = clusterNames
	}
}

func NewTemplateDataFromOptions(queryOptions QueryOptions, options ...TemplateDataOptions) TemplateData {
	templateData := TemplateData{
		NoTruncation:       queryOptions.Limit < 0,
		Limit:              max(queryOptions.Limit, 0),
		TimestampMin:       queryOptions.TimestampMin,
		TimestampMax:       queryOptions.TimestampMax,
		ClusterName:        queryOptions.InfraClusterName,
		SubResourceGroupId: fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", queryOptions.SubscriptionId, queryOptions.ResourceGroupName),
		ResourceGroupName:  queryOptions.ResourceGroupName,
	}
	for _, option := range options {
		option(&templateData)
	}
	return templateData
}

func (d *TemplateData) PreprocessParameterBindings(templatingMode TemplatingMode) map[string]any {
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

		if templatingMode == SafeTemplatingMode {
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

func SetNonEmptyString(kqlParameters *kql.Parameters, fieldName string, value string) {
	if value != "" {
		kqlParameters.AddString(fieldName, value)
	}
}

func SetNonEmptyStringArray(kqlParameters *kql.Parameters, fieldName string, value []string) {
	kqlParameters.AddDynamic(fieldName, value)
}

func (d *TemplateData) CreateKQLParameters() *kql.Parameters {
	kqlParameters := kql.NewParameters()
	SetNonEmptyString(kqlParameters, "table", d.Table)
	SetNonEmptyString(kqlParameters, "clusterName", d.ClusterName)
	SetNonEmptyStringArray(kqlParameters, "clusterNames", d.ClusterNames)
	SetNonEmptyString(kqlParameters, "subResourceGroupId", d.SubResourceGroupId)
	SetNonEmptyString(kqlParameters, "resourceGroupName", d.ResourceGroupName)
	SetNonEmptyString(kqlParameters, "hcpNamespacePrefix", d.HCPNamespacePrefix)
	SetNonEmptyString(kqlParameters, "clusterId", d.ClusterId)
	SetNonEmptyString(kqlParameters, "clusterIds", d.ClusterIds)
	kqlParameters.AddBool("noTruncation", d.NoTruncation)
	kqlParameters.AddInt("limit", int32(d.Limit))
	kqlParameters.AddDateTime("timestampMin", d.TimestampMin)
	kqlParameters.AddDateTime("timestampMax", d.TimestampMax)
	return kqlParameters
}

type TemplatingMode bool

const (
	SafeTemplatingMode   TemplatingMode = true
	UnsafeTemplatingMode TemplatingMode = false
)

// QueryFactory creates Query instances from templates, binding parameters at creation time.
type QueryFactory struct {
	TemplatingMode         TemplatingMode
	MergeStandardProject   bool
	CustomQueryDefinitions []QueryDefinition
}

// NewQueryFactory creates a factory with all the shared query parameters.
func NewQueryFactory(templatingMode TemplatingMode) *QueryFactory {
	customQueryDefinitions, err := LoadCustomQueryDefinitions()
	if err != nil {
		// Panic here, since this should never happen and is obviously a bug
		panic(fmt.Errorf("failed to load custom query definitions: %w", err))
	}
	return &QueryFactory{
		TemplatingMode:         templatingMode,
		CustomQueryDefinitions: customQueryDefinitions,
	}
}

// Build constructs Queries from a QueryDefinition, applying template data and project fields.
// Each template path in the definition produces one Query.
func (f *QueryFactory) Build(def QueryDefinition, templateData TemplateData) ([]Query, error) {
	var queries []Query
	for _, tmplPath := range def.TemplatePaths {
		q, err := f.buildQuery(def.Name, def.Database, tmplPath, templateData, templateData.Limit < 0)
		if err != nil {
			return nil, err
		}
		queries = append(queries, q)
	}
	return queries, nil
}

// buildQuery builds a query from a template, binding parameters at creation time.
// If unsafeTemplating is true, the Parameters are used as is, otherwise they are converted to KQL parameters to avoid injection.
func (f *QueryFactory) buildQuery(name, database, templateName string, data TemplateData, unlimited bool) (*templateQuery, error) {
	templateString := GetTemplate(templateName)

	parameters := data.PreprocessParameterBindings(f.TemplatingMode)

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
	if f.TemplatingMode == SafeTemplatingMode {
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
		name:       def.Name,
		database:   def.Database,
		query:      merged,
		parameters: queries[0].GetParameters(),
		unlimited:  queries[0].IsUnlimited(),
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
