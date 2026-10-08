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

package alertmanagement

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertprocessingrules/armalertprocessingrules"

	"github.com/Azure/ARO-HCP/internal/api/coreapi"
	"github.com/Azure/ARO-HCP/internal/apihelpers/coreapihelpers"
	"github.com/Azure/ARO-HCP/internal/utils"
)

// AlertProcessingRuleListHandler handles GET /admin/v1/alertprocessingrules/.
type AlertProcessingRuleListHandler struct {
	client        *armalertprocessingrules.Client
	resourceGroup string
}

func NewAlertProcessingRuleListHandler(client *armalertprocessingrules.Client, resourceGroup string) *AlertProcessingRuleListHandler {
	return &AlertProcessingRuleListHandler{
		client:        client,
		resourceGroup: resourceGroup,
	}
}

// AlertProcessingRuleGetHandler handles GET /admin/v1/alertprocessingrules/{name}.
type AlertProcessingRuleGetHandler struct {
	client        *armalertprocessingrules.Client
	resourceGroup string
}

func NewAlertProcessingRuleGetHandler(client *armalertprocessingrules.Client, resourceGroup string) *AlertProcessingRuleGetHandler {
	return &AlertProcessingRuleGetHandler{
		client:        client,
		resourceGroup: resourceGroup,
	}
}

// AlertProcessingRulePutHandler handles PUT /admin/v1/alertprocessingrules/{name}.
// It creates or replaces an alert processing rule in the region's resource group.
// amwResourceIds are the region's Azure Monitor Workspace(s), at least one of
// which must be supplied by the client when invoking the PUT operation.
type AlertProcessingRulePutHandler struct {
	client         *armalertprocessingrules.Client
	resourceGroup  string
	amwResourceIds []string
}

func NewAlertProcessingRulePutHandler(client *armalertprocessingrules.Client, resourceGroup string, amwResourceIds []string) *AlertProcessingRulePutHandler {
	h := &AlertProcessingRulePutHandler{
		client:         client,
		resourceGroup:  resourceGroup,
		amwResourceIds: make([]string, len(amwResourceIds)),
	}
	copy(h.amwResourceIds, amwResourceIds)
	slices.Sort(h.amwResourceIds)
	return h
}

// AlertProcessingRuleDeleteHandler handles DELETE /admin/v1/alertprocessingrules/{name}.
type AlertProcessingRuleDeleteHandler struct {
	client        *armalertprocessingrules.Client
	resourceGroup string
}

func NewAlertProcessingRuleDeleteHandler(client *armalertprocessingrules.Client, resourceGroup string) *AlertProcessingRuleDeleteHandler {
	return &AlertProcessingRuleDeleteHandler{
		client:        client,
		resourceGroup: resourceGroup,
	}
}

// AlertProcessingRuleListResponse is returned by the list route.
type AlertProcessingRuleListResponse struct {
	Value []AlertProcessingRuleSummary `json:"value"`
}

// AlertProcessingRuleSummary is the Admin API view of an alert processing rule.
type AlertProcessingRuleSummary struct {
	Name           string            `json:"name"`
	ID             string            `json:"id"`
	Enabled        bool              `json:"enabled"`
	Description    string            `json:"description,omitempty"`
	Scopes         []string          `json:"scopes,omitempty"`
	EffectiveFrom  string            `json:"effectiveFrom,omitempty"`
	EffectiveUntil string            `json:"effectiveUntil,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
}

// AlertProcessingRulePutRequest is the accepted body for a PUT.
type AlertProcessingRulePutRequest struct {
	// AlertRuleName is the alert rule to suppress (matched with Equals). Required.
	AlertRuleName string `json:"alertRuleName"`
	// ResourceFilter, when set, narrows suppression to alerts whose context
	// contains this substring (AlertContext Contains). Optional.
	ResourceFilter string `json:"resourceFilter,omitempty"`
	// StartTime/EndTime bound the suppression window (ISO-8601, no timezone
	// suffix). Both required.
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	// Description and/or IncidentReference document why the rule exists. At least one
	// is required.
	Description       string `json:"description,omitempty"`
	IncidentReference string `json:"incidentReference,omitempty"`
	// Enabled defaults to true when omitted.
	Enabled *bool `json:"enabled,omitempty"`
	// Scopes this rule should apply to, must include at least one known AMW
	Scopes []string `json:"scopes"`
}

const (
	// aroHCPPurposeTagKey/Value mark rules created via this route so the fleet
	// cleanup controller can identify eligible resources.
	aroHCPPurposeTagKey   = "aroHCPPurpose"
	aroHCPPurposeTagValue = "alert-processing-rule"

	// incidentTagKey is used when a user supplies an IncidentReference in a PUT request
	incidentTagKey = "incidentReference"

	// alertProcessingRules are global ARM resources.
	alertProcessingRuleLocation = "Global"

	// monitorConditionFired restricts suppression to firing alerts so resolved
	// notifications still flow (allowing ICM auto-mitigation).
	monitorConditionFired = "Fired"

	// maxPutBodyBytes is a defense-in-depth bound against oversized bodies.
	maxPutBodyBytes int64 = 1 << 20 // 1 MiB
)

func (h *AlertProcessingRuleListHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) error {
	// TODO: we don't anticipate the list of alert suppressions will become too large, if so proper paging will need to be implemented here.
	ctx := request.Context()
	pager := h.client.NewListByResourceGroupPager(h.resourceGroup, nil)

	response := AlertProcessingRuleListResponse{Value: []AlertProcessingRuleSummary{}}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return mapAlertProcessingRuleError(err, nil)
		}
		for _, rule := range page.Value {
			response.Value = append(response.Value, summarizeRule(rule))
		}
	}

	_, writeErr := coreapihelpers.WriteJSONResponse(w, http.StatusOK, response)
	return utils.TrackError(writeErr)
}

func (h *AlertProcessingRuleGetHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	name := request.PathValue("name")

	result, err := h.client.GetByName(ctx, h.resourceGroup, name, nil)
	if err != nil {
		return mapAlertProcessingRuleError(err, &name)
	}

	_, writeErr := coreapihelpers.WriteJSONResponse(w, http.StatusOK, summarizeRule(&result.AlertProcessingRule))
	return utils.TrackError(writeErr)
}

func (h *AlertProcessingRulePutHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	name := request.PathValue("name")

	var body AlertProcessingRulePutRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, request.Body, maxPutBodyBytes)).Decode(&body); err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
			"invalid JSON body: %v", err)
	}
	if body.AlertRuleName == "" {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
			"\"alertRuleName\" is required")
	}
	if body.StartTime == "" || body.EndTime == "" {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
			"\"startTime\" and \"endTime\" are required")
	}
	if body.Description == "" && body.IncidentReference == "" {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
			"at least one of \"description\" or \"incidentReference\" is required")
	}
	scopes, err := h.validateAmwScopes(body.Scopes)
	if err != nil {
		return coreapi.NewCloudError(http.StatusBadRequest, coreapi.CloudErrorCodeInvalidRequestContent, "",
			"Invalid scope(s) in request: %s", err)
	}

	// Match the named alert rule, and only while firing (AC: resolved alerts are
	// not suppressed so ICM tickets can auto-mitigate).
	conditions := []*armalertprocessingrules.Condition{
		{
			Field:    to.Ptr(armalertprocessingrules.FieldAlertRuleName),
			Operator: to.Ptr(armalertprocessingrules.OperatorEquals),
			Values:   []*string{to.Ptr(body.AlertRuleName)},
		},
		{
			Field:    to.Ptr(armalertprocessingrules.FieldMonitorCondition),
			Operator: to.Ptr(armalertprocessingrules.OperatorEquals),
			Values:   []*string{to.Ptr(monitorConditionFired)},
		},
	}
	if body.ResourceFilter != "" {
		conditions = append(conditions, &armalertprocessingrules.Condition{
			Field:    to.Ptr(armalertprocessingrules.FieldAlertContext),
			Operator: to.Ptr(armalertprocessingrules.OperatorContains),
			Values:   []*string{to.Ptr(body.ResourceFilter)},
		})
	}

	tags := map[string]string{aroHCPPurposeTagKey: aroHCPPurposeTagValue}
	if body.IncidentReference != "" {
		tags[incidentTagKey] = body.IncidentReference
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	rule := armalertprocessingrules.AlertProcessingRule{
		Location: to.Ptr(alertProcessingRuleLocation),
		Tags:     toStringPtrMap(tags),
		Properties: &armalertprocessingrules.AlertProcessingRuleProperties{
			Enabled: to.Ptr(enabled),
			Scopes:  to.SliceOfPtrs(scopes...),
			Actions: []armalertprocessingrules.ActionClassification{
				&armalertprocessingrules.RemoveAllActionGroups{},
			},
			Conditions: conditions,
			Schedule: &armalertprocessingrules.Schedule{
				EffectiveFrom:  to.Ptr(body.StartTime),
				EffectiveUntil: to.Ptr(body.EndTime),
			},
		},
	}
	if body.Description != "" {
		rule.Properties.Description = to.Ptr(body.Description)
	}

	result, err := h.client.CreateOrUpdate(ctx, h.resourceGroup, name, rule, nil)
	if err != nil {
		return mapAlertProcessingRuleError(err, &name)
	}

	_, writeErr := coreapihelpers.WriteJSONResponse(w, http.StatusOK, summarizeRule(&result.AlertProcessingRule))
	return utils.TrackError(writeErr)
}

func (h *AlertProcessingRuleDeleteHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) error {
	ctx := request.Context()
	name := request.PathValue("name")

	if _, err := h.client.Delete(ctx, h.resourceGroup, name, nil); err != nil {
		return mapAlertProcessingRuleError(err, &name)
	}

	w.WriteHeader(http.StatusNoContent)
	return nil
}

// mapAlertProcessingRuleError returns a CloudError with the same status code and
// error code as the provided azcore.ResponseError.
func mapAlertProcessingRuleError(err error, name *string) error {
	var azErr *azcore.ResponseError
	if !errors.As(err, &azErr) || azErr == nil {
		return utils.TrackError(err)
	}

	message := fmt.Sprintf("Error processing request: %s", azErr.Error())
	if name != nil {
		message = fmt.Sprintf("Error processing request for rule %s: %s", *name, azErr.Error())
	}

	return coreapi.NewCloudError(azErr.StatusCode, azErr.ErrorCode, "", "%s", message)
}

func summarizeRule(rule *armalertprocessingrules.AlertProcessingRule) AlertProcessingRuleSummary {
	if rule == nil {
		return AlertProcessingRuleSummary{}
	}

	out := AlertProcessingRuleSummary{
		Name: derefString(rule.Name),
		ID:   derefString(rule.ID),
		Tags: derefStringMap(rule.Tags),
	}

	if p := rule.Properties; p != nil {
		out.Enabled = p.Enabled != nil && *p.Enabled
		out.Description = derefString(p.Description)
		for _, s := range p.Scopes {
			out.Scopes = append(out.Scopes, derefString(s))
		}
		if p.Schedule != nil {
			out.EffectiveFrom = derefString(p.Schedule.EffectiveFrom)
			out.EffectiveUntil = derefString(p.Schedule.EffectiveUntil)
		}
	}

	return out
}

// validateAmwScopes validates that the provided scopes are parseable ARM resource
// ids, and that at least one of the scopes matches a preconfigured AMW resource id.
//
// The vast majority of our alerts are Prometheus rule groups. Any alert processing
// rules for these alerts must be scoped to the AMW that contains the alerts to
// be suppressed, so we ensure that at least one of these scopes is always supplied
func (h *AlertProcessingRulePutHandler) validateAmwScopes(scopes []string) ([]string, error) {
	parsedScopes := make([]string, 0, len(scopes))
	errs := make([]error, 0)
	hasAtLeastOneAmwScope := false
	for _, scope := range scopes {
		parsed, err := azcorearm.ParseResourceID(scope)
		if err != nil {
			errs = append(errs, fmt.Errorf("invalid scope %q: %w", scope, err))
			continue
		}
		canonical := parsed.String()
		parsedScopes = append(parsedScopes, canonical)
		if h.matchesConfiguredAmw(canonical) {
			hasAtLeastOneAmwScope = true
		}
	}

	if !hasAtLeastOneAmwScope {
		msg := "at least one of these scopes is required: %s; got: %s"
		configScopes := strings.Join(h.amwResourceIds, ",")
		userScopes := strings.Join(parsedScopes, ",")
		errs = append(errs, fmt.Errorf(msg, configScopes, userScopes))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return parsedScopes, nil
}

// matchesConfiguredAmw reports whether scope refers to one of the pre-configured
// AMW resource IDs. ARM resource IDs are case-insensitive, so the comparison uses
// strings.EqualFold rather than an exact lookup.
func (h *AlertProcessingRulePutHandler) matchesConfiguredAmw(scope string) bool {
	for _, amw := range h.amwResourceIds {
		if strings.EqualFold(amw, scope) {
			return true
		}
	}
	return false
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefStringMap(in map[string]*string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = derefString(v)
	}
	return out
}

func toStringPtrMap(in map[string]string) map[string]*string {
	if in == nil {
		return nil
	}
	out := make(map[string]*string, len(in))
	for k, v := range in {
		val := v
		out[k] = &val
	}
	return out
}
