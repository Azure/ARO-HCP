package azureutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	"k8s.io/utils/ptr"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type roleAssignmentTransport struct {
	do func(*http.Request) (*http.Response, error)
}

func (transport roleAssignmentTransport) Do(request *http.Request) (*http.Response, error) {
	return transport.do(request)
}

func TestDeleteRoleAssignmentOps(t *testing.T) {
	const subscriptionID = "00000000-0000-0000-0000-0000000000ab"
	const groupScope = "/subscriptions/" + subscriptionID + "/resourceGroups/fake-group"
	const resourceScope = groupScope + "/providers/Microsoft.Network/virtualNetworks/fake-network"
	for _, testCase := range []struct {
		name          string
		scope         string
		deleteStatus  int
		deleteCode    string
		lookupStatus  int
		lookupCode    string
		lookupError   error
		wantGets      int
		wantSuccess   bool
		nilProperties bool
		nilScope      bool
		nilClient     bool
	}{
		{name: "missing group", scope: groupScope, wantGets: 1, wantSuccess: true},
		{name: "missing resource parent group", scope: resourceScope, wantGets: 1, wantSuccess: true},
		{name: "case insensitive scope", scope: strings.ToUpper(resourceScope), wantGets: 1, wantSuccess: true},
		{name: "present group", scope: groupScope, lookupStatus: http.StatusOK, wantGets: 1},
		{name: "lookup forbidden", scope: groupScope, lookupStatus: http.StatusForbidden, lookupCode: "AuthorizationFailed", wantGets: 1},
		{name: "lookup server error", scope: groupScope, lookupStatus: http.StatusInternalServerError, lookupCode: "InternalServerError", wantGets: 1},
		{name: "lookup transport error", scope: groupScope, lookupError: errors.New("fake lookup transport failure"), wantGets: 1},
		{name: "lookup canceled", scope: groupScope, lookupError: context.Canceled, wantGets: 1},
		{name: "ambiguous not found", scope: groupScope, lookupCode: "ResourceNotFound", wantGets: 1},
		{name: "wrong status for missing group", scope: groupScope, lookupStatus: http.StatusForbidden, lookupCode: "ResourceGroupNotFound", wantGets: 1},
		{name: "unrelated forbidden", scope: groupScope, deleteCode: "Forbidden"},
		{name: "delete server error", scope: groupScope, deleteStatus: http.StatusInternalServerError, deleteCode: "InternalServerError"},
		{name: "successful delete", scope: groupScope, deleteStatus: http.StatusOK, wantSuccess: true},
		{name: "already deleted", scope: groupScope, deleteStatus: http.StatusNotFound, deleteCode: "ResourceNotFound", wantSuccess: true},
		{name: "subscription scope", scope: "/subscriptions/" + subscriptionID},
		{name: "tenant scope", scope: "/"},
		{name: "management group scope", scope: "/providers/Microsoft.Management/managementGroups/fake-group"},
		{name: "different subscription", scope: strings.ReplaceAll(groupScope, subscriptionID, "00000000-0000-0000-0000-0000000000cd")},
		{name: "invalid subscription", scope: strings.ReplaceAll(groupScope, subscriptionID, "bad-subscription")},
		{name: "empty scope"},
		{name: "missing group name", scope: "/subscriptions/" + subscriptionID + "/resourceGroups/"},
		{name: "missing resource name", scope: groupScope + "/providers/Microsoft.Network/virtualNetworks"},
		{name: "duplicate slash", scope: strings.ReplaceAll(groupScope, "/resourceGroups", "//resourceGroups")},
		{name: "trailing slash", scope: groupScope + "/"},
		{name: "query", scope: groupScope + "?fake=query"},
		{name: "escaped slash", scope: groupScope + "%2Fother"},
		{name: "relative segment", scope: groupScope + "/providers/Microsoft.Network/virtualNetworks/.."},
		{name: "non provider child", scope: groupScope + "/invalid/child"},
		{name: "missing properties", scope: groupScope, nilProperties: true},
		{name: "missing scope", scope: groupScope, nilScope: true},
		{name: "missing client", scope: groupScope, nilClient: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			deleteStatus, deleteCode := testCase.deleteStatus, testCase.deleteCode
			if deleteStatus == 0 {
				deleteStatus = http.StatusForbidden
			}
			if deleteCode == "" {
				deleteCode = "AuthorizationFailed"
			}
			gets, deletes := 0, 0
			transport := roleAssignmentTransport{do: func(request *http.Request) (*http.Response, error) {
				status, code, message := deleteStatus, deleteCode, "original fake delete failure"
				switch request.Method {
				case http.MethodDelete:
					deletes++
				case http.MethodGet:
					gets++
					if !strings.EqualFold(request.URL.Path, groupScope) {
						t.Fatalf("GET must target assignment group in correct subscription, got %s", request.URL.Path)
					}
					if testCase.lookupError != nil {
						return nil, testCase.lookupError
					}
					status, code, message = testCase.lookupStatus, testCase.lookupCode, "fake lookup failure"
					if status == 0 {
						status = http.StatusNotFound
					}
					if code == "" {
						code = "ResourceGroupNotFound"
					}
				default:
					t.Fatalf("unexpected ARM method %s", request.Method)
				}
				body := `{}`
				if status >= http.StatusBadRequest {
					payload, err := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
					if err != nil {
						t.Fatal(err)
					}
					body = string(payload)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			}}
			options := &azcorearm.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1}}}
			roles, err := armauthorization.NewRoleAssignmentsClient(subscriptionID, &azfake.TokenCredential{}, options)
			if err != nil {
				t.Fatal(err)
			}
			groups, err := armresources.NewResourceGroupsClient(subscriptionID, &azfake.TokenCredential{}, options)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.nilClient {
				groups = nil
			}
			assignment := &armauthorization.RoleAssignment{
				ID:         ptr.To(groupScope + "/providers/Microsoft.Authorization/roleAssignments/00000000-0000-0000-0000-000000000001"),
				Properties: &armauthorization.RoleAssignmentProperties{Scope: ptr.To(testCase.scope), PrincipalID: ptr.To("00000000-0000-0000-0000-000000000002")},
			}
			if testCase.nilScope {
				assignment.Properties.Scope = nil
			}
			if testCase.nilProperties {
				assignment.Properties = nil
			}
			operations := DeleteRoleAssignmentOps(logr.Discard(), roles, groups, subscriptionID, []*armauthorization.RoleAssignment{assignment})
			err = operations[0](t.Context())
			if testCase.wantSuccess {
				if err != nil {
					t.Fatalf("expected cleanup success, got %v", err)
				}
			} else {
				var responseError *azcore.ResponseError
				if !errors.As(err, &responseError) || responseError.StatusCode != deleteStatus || responseError.ErrorCode != deleteCode || !strings.Contains(err.Error(), "original fake delete failure") || !strings.Contains(err.Error(), fmt.Sprintf("failed deleting role assignment %q", *assignment.ID)) {
					t.Fatalf("expected original wrapped delete failure (%d, %s), got %v", deleteStatus, deleteCode, err)
				}
			}
			if deletes != 1 || gets != testCase.wantGets {
				t.Fatalf("expected 1 DELETE and %d GETs, got %d DELETEs and %d GETs", testCase.wantGets, deletes, gets)
			}
		})
	}
}
