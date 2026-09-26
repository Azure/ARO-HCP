// Copyright 2026 Microsoft Corporation
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

package gatherobservability

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/prometheusrulegroups/armprometheusrulegroups"
)

func TestCollectAlertRules(t *testing.T) {
	ws, err := azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Monitor/accounts/workspace")
	if err != nil {
		t.Fatal(err)
	}
	group := &armprometheusrulegroups.PrometheusRuleGroupResource{
		ID: to.Ptr("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.AlertsManagement/prometheusRuleGroups/group"),
		Properties: &armprometheusrulegroups.PrometheusRuleGroupProperties{
			Scopes:   []*string{nil, to.Ptr(strings.ToUpper(ws.String()))},
			Interval: to.Ptr("PT30S"),
			Rules: []*armprometheusrulegroups.PrometheusRule{
				nil,
				{Record: to.Ptr("recorded_metric"), Expression: to.Ptr("up")},
				{Alert: to.Ptr("")},
				{Alert: to.Ptr("Zebra"), Expression: to.Ptr("up == 0")},
				{Alert: to.Ptr("Alpha"), Expression: to.Ptr("up > 1")},
			},
		},
	}
	pages := []armprometheusrulegroups.ClientListByResourceGroupResponse{
		{PrometheusRuleGroupResourceCollection: armprometheusrulegroups.PrometheusRuleGroupResourceCollection{
			Value: []*armprometheusrulegroups.PrometheusRuleGroupResource{
				nil, {},
				{Properties: &armprometheusrulegroups.PrometheusRuleGroupProperties{
					Scopes: []*string{to.Ptr(ws.String() + "-other")},
					Rules:  []*armprometheusrulegroups.PrometheusRule{{Alert: to.Ptr("WrongWorkspace")}},
				}},
				group,
			},
		}},
		{PrometheusRuleGroupResourceCollection: armprometheusrulegroups.PrometheusRuleGroupResourceCollection{
			Value: []*armprometheusrulegroups.PrometheusRuleGroupResource{
				{Properties: &armprometheusrulegroups.PrometheusRuleGroupProperties{
					Scopes: []*string{to.Ptr(ws.String())},
					Rules:  []*armprometheusrulegroups.PrometheusRule{{Alert: to.Ptr("Alpha")}},
				}},
			},
		}},
	}
	want := alertRuleInventory{
		Names: []string{"Alpha", "Zebra"},
		Definitions: []alertRuleDefinition{
			{Name: "Zebra", Expression: "up == 0", Interval: "PT30S", GroupID: *group.ID},
			{Name: "Alpha", Expression: "up > 1", Interval: "PT30S", GroupID: *group.ID},
			{Name: "Alpha"},
		},
	}
	for _, failAfterPages := range []int{-1, 0, 2} {
		t.Run(map[int]string{-1: "success", 0: "first_page_error", 2: "partial_results"}[failAfterPages], func(t *testing.T) {
			fetchErr := errors.New("page unavailable")
			next := 0
			pager := runtime.NewPager(runtime.PagingHandler[armprometheusrulegroups.ClientListByResourceGroupResponse]{
				More: func(armprometheusrulegroups.ClientListByResourceGroupResponse) bool {
					return next < len(pages) || next == failAfterPages
				},
				Fetcher: func(context.Context, *armprometheusrulegroups.ClientListByResourceGroupResponse) (armprometheusrulegroups.ClientListByResourceGroupResponse, error) {
					if next == failAfterPages {
						return armprometheusrulegroups.ClientListByResourceGroupResponse{}, fetchErr
					}
					page := pages[next]
					next++
					return page, nil
				},
			})
			got, err := collectAlertRules(context.Background(), pager, *ws)
			if (failAfterPages >= 0 && !errors.Is(err, fetchErr)) || (failAfterPages < 0 && err != nil) {
				t.Fatalf("unexpected collection error: %v", err)
			}
			expected := want
			if failAfterPages == 0 {
				expected = alertRuleInventory{}
			}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("inventory = %#v, want %#v", got, expected)
			}
		})
	}
}

func TestDiagnosticStep(t *testing.T) {
	const groupID = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.AlertsManagement/prometheusRuleGroups/group"
	base := alertRuleDefinition{Name: "Down", Expression: `up{job="api",cluster="svc"} == 0`, Interval: "PT30S", GroupID: groupID}
	otherGroup := base
	otherGroup.GroupID += "-other"
	otherGroup.Interval = "PT2M"
	changed := base
	changed.Expression = `up{job="api",cluster="svc"} == 1`
	invalid := base
	invalid.Interval = ""
	invalidExpression := base
	invalidExpression.Expression = "up =="
	otherName := base
	otherName.Name = "Other"
	equivalentInterval := base
	equivalentInterval.GroupID += "-other"
	equivalentInterval.Interval = "PT0M30S"
	for _, tt := range []struct {
		name        string
		ruleID      string
		expression  string
		definitions []alertRuleDefinition
		want        string
		warning     bool
	}{
		{name: "normalized", definitions: []alertRuleDefinition{base}, want: "30s"},
		{name: "known_group", ruleID: strings.ToUpper(groupID), definitions: []alertRuleDefinition{otherGroup, base}, want: "30s"},
		{name: "known_group_changed", ruleID: groupID, definitions: []alertRuleDefinition{changed, otherGroup}, want: "60s", warning: true},
		{name: "known_group_missing", ruleID: groupID, definitions: []alertRuleDefinition{otherGroup}, want: "60s", warning: true},
		{name: "ambiguous", definitions: []alertRuleDefinition{base, otherGroup}, want: "60s", warning: true},
		{name: "same_interval", definitions: []alertRuleDefinition{base, equivalentInterval}, want: "30s"},
		{name: "invalid_duplicate", definitions: []alertRuleDefinition{base, invalid}, want: "60s", warning: true},
		{name: "changed_expression", definitions: []alertRuleDefinition{changed}, want: "60s", warning: true},
		{name: "invalid_definition_expression", definitions: []alertRuleDefinition{invalidExpression}, want: "60s", warning: true},
		{name: "wrong_name", definitions: []alertRuleDefinition{otherName}, want: "60s", warning: true},
		{name: "missing_metadata", want: "60s", warning: true},
		{name: "invalid_alert_expression", expression: "up ==", definitions: []alertRuleDefinition{invalidExpression}, want: "60s", warning: true},
		{name: "empty_alert_expression", expression: " ", definitions: []alertRuleDefinition{base}, want: "60s", warning: true},
		{name: "scalar_alert_expression", expression: "1", definitions: []alertRuleDefinition{base}, want: "60s", warning: true},
		{name: "display_name_not_group_id", ruleID: "group", definitions: []alertRuleDefinition{base, otherGroup}, want: "60s", warning: true},
		{name: "unknown_child_format", ruleID: groupID + "/rules/Down", definitions: []alertRuleDefinition{base, otherGroup}, want: "60s", warning: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			expression := tt.expression
			if expression == "" {
				expression = "up{cluster=\"svc\", job=\"api\"}==0\n"
			}
			step, warning := diagnosticStep(alert{Alert: alertData{Name: "Down", Expression: expression, AlertRule: tt.ruleID}}, tt.definitions)
			if step != tt.want || (warning != "") != tt.warning {
				t.Fatalf("diagnosticStep = (%q, %q), want (%q, warning=%t)", step, warning, tt.want, tt.warning)
			}
		})
	}
}

func TestDiagnosticStepInterval(t *testing.T) {
	for _, tt := range []struct{ interval, want string }{
		{"PT1M", "60s"}, {"PT30S", "30s"}, {"PT1H30M", "5400s"},
		{"PT1H2M3S", "3723s"}, {"PT0.001S", "1ms"}, {"PT1.5S", "1500ms"},
		{"PT0S", ""}, {"PT", ""}, {"", ""}, {"PT-1M", ""}, {"-PT1M", ""},
		{"P1M", ""}, {"P1Y", ""}, {"P1DT1H", ""}, {"PT1M1H", ""},
		{"PT1S1S", ""}, {"PT1.5M", ""}, {"PT0.0001S", ""},
		{"PT999999999999999999999999H", ""}, {"PT2562048H", ""},
		{"pt1m", ""}, {" PT1M", ""}, {"PT1M\n", ""}, {"junkPT1M", ""},
	} {
		t.Run(tt.interval, func(t *testing.T) {
			step, warning := diagnosticStep(alert{Alert: alertData{Name: "Down", Expression: "up == 0"}}, []alertRuleDefinition{
				{Name: "Down", Expression: "up == 0", Interval: tt.interval},
			})
			want := tt.want
			if want == "" {
				want = "60s"
			}
			if step != want || (warning != "") != (tt.want == "") {
				t.Fatalf("diagnosticStep interval %q = (%q, %q), want %q (warning=%t)", tt.interval, step, warning, want, tt.want == "")
			}
		})
	}
}
