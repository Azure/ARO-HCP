package hcpresourcerequirements

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/Azure/ARO-HCP/internal/api/fleetapi"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/cosmosstorageutils"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstorage/fleetcosmosstorage"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/fleetlistertesting"
	"github.com/Azure/ARO-HCP/internal/database/listertesting/kubeapplierlistertesting"
)

type failingRequirementsCRUD struct {
	cosmosstorageutils.ResourceCRUD[fleetapi.HCPResourceRequirements, *fleetapi.HCPResourceRequirements]
	fail      bool
	attempted map[string]int
}

func (crud *failingRequirementsCRUD) Replace(ctx context.Context, document *fleetapi.HCPResourceRequirements, options *azcosmos.ItemOptions) (*fleetapi.HCPResourceRequirements, error) {
	name := document.ResourceID.Name
	crud.attempted[name]++
	if crud.fail && (name == "a" || name == "b") {
		return nil, fmt.Errorf("injected failure for %s", name)
	}
	return crud.ResourceCRUD.Replace(ctx, document, options)
}

type failingRequirementsDB struct {
	fleetcosmosstorage.FleetDBClient
	crud *failingRequirementsCRUD
}

func (database *failingRequirementsDB) HCPResourceRequirements() cosmosstorageutils.ResourceCRUD[fleetapi.HCPResourceRequirements, *fleetapi.HCPResourceRequirements] {
	return database.crud
}

func TestAllConfigurationsAttemptedAndRetryConverges(t *testing.T) {
	ctx := t.Context()
	database := fleetcosmosstoragetesting.NewMockFleetDBClient()
	controller := NewController(time.Minute, database, &kubeapplierlistertesting.SliceReadDesireLister{}, &fleetlistertesting.SliceStampLister{})
	first := metav1.Now()
	condition := metav1.Condition{Type: fleetapi.ConditionTypeSamplesAvailable, Status: metav1.ConditionTrue, Reason: "Computed"}
	for _, name := range []string{"a", "b", "c", "default"} {
		require.NoError(t, controller.persist(ctx, name, readyTotals("2").Usage, readyTotals("2").Requested, 1, &first, condition))
	}
	crud := &failingRequirementsCRUD{ResourceCRUD: database.HCPResourceRequirements(), fail: true, attempted: map[string]int{}}
	controller.fleetDBClient = &failingRequirementsDB{FleetDBClient: database, crud: crud}
	err := controller.syncOnce(ctx)
	require.ErrorContains(t, err, `configuration "a"`)
	require.ErrorContains(t, err, `configuration "b"`)
	require.Equal(t, map[string]int{"a": 1, "b": 1, "c": 1, "default": 1}, crud.attempted)
	versions := map[string]string{}
	for _, name := range []string{"c", "default"} {
		document, err := database.HCPResourceRequirements().Get(ctx, name)
		require.NoError(t, err)
		versions[name] = string(document.CosmosETag)
	}
	crud.fail = false
	require.NoError(t, controller.syncOnce(ctx))
	require.Equal(t, map[string]int{"a": 2, "b": 2, "c": 1, "default": 1}, crud.attempted)
	for _, name := range []string{"a", "b", "c", "default"} {
		document, err := database.HCPResourceRequirements().Get(ctx, name)
		require.NoError(t, err)
		require.Nil(t, document.Status.AverageUsage)
		require.Nil(t, document.Status.AverageRequests)
		require.Zero(t, document.Status.SampleSize)
		require.Equal(t, "NoSamples", document.Status.Conditions[0].Reason)
		if previous, ok := versions[name]; ok {
			require.Equal(t, previous, string(document.CosmosETag))
		}
	}
}
