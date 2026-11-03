package azureresourcesapihelpers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Azure/ARO-HCP/internal/api/azureresourcesapi"
	azcorearm "github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeploymentstacks/v2"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

func TestDeploymentStackDesireSpecHash(t *testing.T) {
	target, err := azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Resources/deploymentStacks/stack")
	require.NoError(t, err)
	spec := azureresourcesapi.DeploymentStackDesireSpec{
		TargetResourceID: target,
		Authentication:   azureresourcesapi.DeploymentStackDesireAuthentication{Type: azureresourcesapi.DeploymentStackDesireAuthenticationTypeFPA},
		Action:           azureresourcesapi.DeploymentStackDesireActionApply,
		Stack: &armdeploymentstacks.DeploymentStack{Properties: &armdeploymentstacks.DeploymentStackProperties{
			Template: map[string]any{"b": float64(42), "a": []any{float64(1), float64(2)}},
		}},
	}
	hash, err := DeploymentStackDesireSpecHash(spec)
	require.NoError(t, err)
	require.Len(t, hash, 64)
	for _, stack := range []string{
		` { "properties": { "template": { "a": [1, 2], "b": 42 } } } `,
		`{"properties":{"template":{"a":[1,2],"\u0062":42}}}`,
	} {
		other := *spec.DeepCopy()
		require.NoError(t, json.Unmarshal([]byte(stack), &other.Stack))
		actual, err := DeploymentStackDesireSpecHash(other)
		require.NoError(t, err)
		require.Equal(t, hash, actual, "object order, whitespace and string escaping must not affect the hash")
	}
	for name, mutate := range map[string]func(*azureresourcesapi.DeploymentStackDesireSpec){
		"array order": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.Stack.Properties.Template.(map[string]any)["a"] = []any{float64(2), float64(1)}
		},
		"template": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.Stack.Properties.Template.(map[string]any)["b"] = float64(43)
		},
		"delete policy": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.Stack.Properties.ActionOnUnmanage = &armdeploymentstacks.ActionOnUnmanage{Resources: ptr.To(armdeploymentstacks.UnmanageActionResourceModeDelete)}
		},
		"bypass out of sync": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.Stack.Properties.BypassStackOutOfSyncError = ptr.To(true)
		},
		"target": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.TargetResourceID, _ = azcorearm.ParseResourceID(target.String() + "-other")
		},
		"action": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.Action = azureresourcesapi.DeploymentStackDesireActionDelete
		},
		"authentication type": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.Authentication.Type = azureresourcesapi.DeploymentStackDesireAuthenticationTypeSMI
		},
		"tenant": func(spec *azureresourcesapi.DeploymentStackDesireSpec) { spec.Authentication.TenantID = "tenant" },
		"SMI": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.Authentication.SMIResourceID, _ = azcorearm.ParseResourceID("/subscriptions/sub/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/smi")
		},
		"identity URL": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.Authentication.ClusterIdentityURL = "https://identity.example.com"
		},
		"interval": func(spec *azureresourcesapi.DeploymentStackDesireSpec) {
			spec.ReconcileInterval.Duration = 6 * time.Hour
		},
	} {
		t.Run(name, func(t *testing.T) {
			other := spec.DeepCopy()
			mutate(other)
			actual, err := DeploymentStackDesireSpecHash(*other)
			require.NoError(t, err)
			require.NotEqual(t, hash, actual, "the whole spec must contribute to the hash")
		})
	}
	spec.Stack.Properties.Template = map[string]any{"number": json.Number("9007199254740993")}
	hash, err = DeploymentStackDesireSpecHash(spec)
	require.NoError(t, err)
	spec.Stack.Properties.Template = map[string]any{"number": json.Number("9007199254740992")}
	otherHash, err := DeploymentStackDesireSpecHash(spec)
	require.NoError(t, err)
	require.NotEqual(t, hash, otherHash, "canonicalization must retain the numbers marshaled by the SDK")
	spec.Stack.Properties.Template = make(chan int)
	_, err = DeploymentStackDesireSpecHash(spec)
	require.Error(t, err, "non-JSON SDK data must fail hashing")
}
