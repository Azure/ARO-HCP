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

package app

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	clocktesting "k8s.io/utils/clock/testing"

	apisconfigv1 "github.com/Azure/ARO-HCP/backend/pkg/apis/config/v1"
	azureconfig "github.com/Azure/ARO-HCP/backend/pkg/azure/config"
	clusterbackups "github.com/Azure/ARO-HCP/backend/pkg/controllers/cluster/backups"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/billingcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/corecosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/fleetcosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/cosmosstoragetesting/kubeappliercosmosstoragetesting"
	"github.com/Azure/ARO-HCP/internal/database/informers/coreinformers"
	"github.com/Azure/ARO-HCP/internal/database/informers/fleetinformers"
)

var expectedControllerLaunches = []struct {
	name    string
	workers int
}{
	{"union-kube-applier-informers-controller", 1},
	{"subscriptionnonclusterdatadump", 20},
	{"datadump", 20},
	{"csstatedump", 20},
	{"billingdump", 20},
	{"managementclusterdatadump", 20},
	{"dispatchrequestcredential", 20},
	{"systemadmincredentialdispatchrequestcredential", 20},
	{"systemadmincredentialdispatchrevokecredentials", 20},
	{"systemadmincredentialoperationrequestcredentialpoll", 20},
	{"systemadmincredentialoperationrevokecredentialspoll", 20},
	{"systemadmincredentialissuanceobserver", 20},
	{"systemadmincredentialdesirescreator", 20},
	{"systemadmincredentialpostissuancecleanup", 20},
	{"systemadmincredentialrevokedgc", 20},
	{"systemadmincredentialclusterdeletioncleanup", 20},
	{"systemadmincredentialrevocationmarkrequests", 20},
	{"systemadmincredentialrevocationdesires", 20},
	{"systemadmincredentialrevocationcompletion", 20},
	{"systemadmincredentialrevocationdeletion", 20},
	{"clusterdenyassignment", 20},
	{"clusterpendingclusterserviceidassign", 20},
	{"clusterclusterservicecreate", 20},
	{"nodepoolclusterservicecreate", 20},
	{"externalauthclusterservicecreate", 20},
	{"operationclustercreate", 20},
	{"operationclusterupdate", 20},
	{"operationclusterdelete", 20},
	{"operationnodepoolcreate", 20},
	{"operationnodepoolupdate", 20},
	{"operationnodepooldelete", 20},
	{"operationexternalauthcreate", 20},
	{"operationexternalauthupdate", 20},
	{"operationexternalauthdelete", 20},
	{"operationrequestcredential", 20},
	{"clusterservicematchingclusters", 20},
	{"clustervalidationalwayssuccessvalidation", 20},
	{"deleteorphanedcosmosresources", 10},
	{"missingresourceid", 20},
	{"backfillclusteruid", 20},
	{"orphanedbillingcleanup", 20},
	{"createbillingdoc", 20},
	{"controlplaneactiveversions", 20},
	{"controlplanedesiredversion", 20},
	{"triggercontrolplaneupgrade", 20},
	{"clusterbasedomainprefixsync", 20},
	{"clusterpropertiessync", 20},
	{"clusteridentitysync", 20},
	{"clusterdegradedaggregator", 20},
	{"clusterrequirementsvalidaggregator", 20},
	{"nodepooldegradedaggregator", 20},
	{"nodepoolrequirementsvalidaggregator", 20},
	{"externalauthdegradedaggregator", 20},
	{"desiredcontrolplanesize", 20},
	{"serviceproviderclusterpropertiessync", 20},
	{"clustervalidationazureresourceprovidersregistrationvalidation", 20},
	{"clustervalidationazureclusterresourcegroupexistencevalidation", 20},
	{"clustervalidationazureclustermanagedidentitiesexistencevalidation", 20},
	{"nodepoolvalidationazurevmsizesupportsephemeralosdiskvalidation", 20},
	{"nodepoolvalidationazurenodepoolvmquotavalidation", 20},
	{"clustervalidationcontrolplaneidentitiespermissionsclustervalidation", 20},
	{"nodepoolvalidationazurenodepoolnsgbasedrequiredconnectivityvalidation", 20},
	{"clustervalidationdataplaneidentitiespermissionsvalidation", 20},
	{"clustervalidationcontainerregistrypullcredentialspermissionvalidation", 20},
	{"nodepoolversion", 20},
	{"nodepoolactiveversions", 20},
	{"createclusterscopedreaddesires", 20},
	{"createnodepoolscopedreaddesires", 20},
	{"createserviceprovidercluster", 20},
	{"createserviceprovidernodepool", 20},
	{"cleanorphanedclustermanagedresourcegroup", 20},
	{"ensuremanagedresourcegroup", 20},
	{"triggernodepoolupgrade", 20},
	{"nodepoolclusterservicedeletedispatch", 20},
	{"nodepooldeletionclusterserviceidclearer", 20},
	{"nodepoolchildresourcescleanupcontroller", 20},
	{"nodepooldeletioncontroller", 20},
	{"externalauthclusterservicedeletedispatch", 20},
	{"externalauthdeletionclusterserviceidclearer", 20},
	{"externalauthchildresourcescleanupcontroller", 20},
	{"externalauthdeletioncontroller", 20},
	{"clusterclusterservicedeletedispatch", 20},
	{"clusterdeletionclusterserviceidclearer", 20},
	{"clustercredentialdeletionmarkercontroller", 20},
	{"clusterchildresourcescleanupcontroller", 20},
	{"clusterdeletioncontroller", 20},
	{"clusterclusterserviceupdatedispatch", 20},
	{"nodepoolclusterserviceupdatedispatch", 20},
	{"externalauthclusterserviceupdatedispatch", 20},
	{"operationphasemetrics", 1},
	{"clustermetrics", 1},
	{"clusterversionmetrics", 1},
	{"nodepoolmetrics", 1},
	{"externalauthmetrics", 1},
	{"clusterinfometrics", 1},
	{"managementclusterplacementsync", 20},
	{"placement", 20},
	{"pendingcleanup", 5},
	{"cosmosmigration", 5},
	{"fpavirtualmachineresourceskuscachedreader", 20},
	{"backupschedule", 20},
	{"fetchmsiidentitiesinfo", 20},
	{"fetchdataplaneoperatorsmanagedidentitiesinfo", 20},
	{"identityroleassignments", 20},
	{"keyrotationbackup", 20},
	{"clusterresources", 20},
}

func TestControllerRegistryManifest(t *testing.T) {
	registry := newControllerRegistry()
	require.Len(t, registry, 106)
	expectedOrder := make([]string, 0, len(expectedControllerLaunches))
	for _, expected := range expectedControllerLaunches {
		expectedOrder = append(expectedOrder, expected.name)
		entry, exists := registry[expected.name]
		require.True(t, exists, "missing controller %s", expected.name)
		require.Equal(t, expected.name, strings.ToLower(expected.name))
		require.Equal(t, expected.workers, entry.Workers, expected.name)
		require.NotNil(t, entry.Instantiate, expected.name)

		if expected.name == "clusterdenyassignment" {
			require.NotNil(t, entry.Enabled)
			require.False(t, entry.Enabled(ControllerContext{}))
			require.True(t, entry.Enabled(ControllerContext{HasRealFPA: true}))
		} else {
			require.Nil(t, entry.Enabled, expected.name)
		}
	}
	require.ElementsMatch(t, expectedOrder, slices.Collect(maps.Keys(registry)))
}

func testControllerContext(t *testing.T, hasRealFPA bool) ControllerContext {
	t.Helper()
	cloudEnvironment, err := azureconfig.NewAzureCloudEnvironment(apisconfigv1.AzurePublicCloud, nil)
	require.NoError(t, err)
	backend := &Backend{
		clock: clocktesting.NewFakePassiveClock(time.Now()),
		options: &BackendOptions{
			ResourcesDBClient:    corecosmosstoragetesting.NewMockResourcesDBClient(),
			BillingDBClient:      billingcosmosstoragetesting.NewMockBillingDBClient(),
			FleetDBClient:        fleetcosmosstoragetesting.NewMockFleetDBClient(),
			KubeApplierDBClients: kubeappliercosmosstoragetesting.NewMockKubeApplierDBClients(),
			MetricsRegisterer:    prometheus.NewRegistry(),
			BackupConfig:         &clusterbackups.BackupConfig{},
			CloudEnvironment:     cloudEnvironment,
			HasRealFPA:           hasRealFPA,
		},
	}
	controllerContext := backend.newControllerContext(t.Context())
	require.Same(t, backend.clock, controllerContext.Clock)
	require.Same(t, http.DefaultClient, controllerContext.AsyncOperationNotificationClient)
	require.Same(t, backend.options.ResourcesDBClient, controllerContext.ResourcesDBClient)
	return controllerContext
}

func TestControllerRegistryInstantiation(t *testing.T) {
	for _, hasRealFPA := range []bool{false, true} {
		t.Run(fmt.Sprintf("HasRealFPA=%t", hasRealFPA), func(t *testing.T) {
			controllerContext := testControllerContext(t, hasRealFPA)
			controllers, err := instantiateControllers(newControllerRegistry(), controllerContext)
			require.NoError(t, err)
			expected := expectedControllerLaunches
			if !hasRealFPA {
				expected = nil
				for _, entry := range expectedControllerLaunches {
					if entry.name != "clusterdenyassignment" {
						expected = append(expected, entry)
					}
				}
			}
			require.Len(t, controllers, len(expected))
			for name, controller := range controllers {
				require.Equal(t, name, controller.name)
				require.Equal(t, newControllerRegistry()[name].Workers, controller.workers)
				require.NotNil(t, controller.runnable, controller.name)
				if name != "union-kube-applier-informers-controller" && name != "fpavirtualmachineresourceskuscachedreader" {
					waiter := reflect.ValueOf(controller.runnable).Elem().FieldByName("CacheSyncWaiter")
					require.True(t, waiter.IsValid(), "missing cache gate for %s", name)
					if name == "missingresourceid" {
						require.Zero(t, waiter.FieldByName("cacheSyncs").Len(), "this controller uses only live DB reads")
					} else {
						require.Positive(t, waiter.FieldByName("cacheSyncs").Len(), "no cache dependencies for %s", name)
					}
				}
				if controller.name != "union-kube-applier-informers-controller" {
					actualName := reflect.ValueOf(controller.runnable).Elem().FieldByName("name")
					require.True(t, actualName.IsValid(), controller.name)
					require.Equal(t, controller.name, strings.ToLower(actualName.String()))
				}
			}
		})
	}
}

func TestControllerRegistrySharedInstances(t *testing.T) {
	controllerContext := testControllerContext(t, true)
	registry := newControllerRegistry()
	unionController, err := registry["union-kube-applier-informers-controller"].Instantiate(controllerContext)
	require.NoError(t, err)
	require.Same(t, controllerContext.UnionKubeApplierInformersController, unionController)
	require.Same(t, controllerContext.UnionKubeApplierInformers, controllerContext.UnionKubeApplierInformersController.Union())
	_, readLister := controllerContext.UnionKubeApplierInformers.ReadDesires()
	_, applyLister := controllerContext.UnionKubeApplierInformers.ApplyDesires()
	_, clusterLister := controllerContext.BackendInformers.Clusters()
	_, serviceProviderClusterLister := controllerContext.BackendInformers.ServiceProviderClusters()
	activeVersionsController, err := registry["controlplaneactiveversions"].Instantiate(controllerContext)
	require.NoError(t, err)
	requireControllerDependency(t, activeVersionsController, clusterLister, "syncer", "syncer", "clusterLister")
	requireControllerDependency(t, activeVersionsController, serviceProviderClusterLister, "syncer", "syncer", "serviceProviderClusterLister")
	requireControllerDependency(t, activeVersionsController, readLister, "syncer", "syncer", "readDesireLister")
	revocationController, err := registry["systemadmincredentialrevocationdesires"].Instantiate(controllerContext)
	require.NoError(t, err)
	requireControllerDependency(t, revocationController, applyLister, "syncer", "syncer", "applyDesireLister")
	requireControllerDependency(t, revocationController, readLister, "syncer", "syncer", "readDesireLister")
	managementInformer, managementLister := controllerContext.FleetInformers.ManagementClusters()
	requireControllerDependency(t, unionController, managementInformer, "mcInformer")
	requireControllerDependency(t, unionController, managementLister, "mcLister")
	dumpController, err := registry["datadump"].Instantiate(controllerContext)
	require.NoError(t, err)
	requireControllerDependency(t, dumpController, managementLister, "syncer", "syncer", "managementClusterLister")
	skuController, err := registry["fpavirtualmachineresourceskuscachedreader"].Instantiate(controllerContext)
	require.NoError(t, err)
	require.Same(t, controllerContext.VirtualMachineResourceSKUsCachedReaderController, skuController)
	for _, name := range []string{"nodepoolvalidationazurevmsizesupportsephemeralosdiskvalidation", "nodepoolvalidationazurenodepoolvmquotavalidation"} {
		controller, err := registry[name].Instantiate(controllerContext)
		require.NoError(t, err)
		requireControllerDependency(t, controller, skuController, "syncer", "syncer", "validation", "resourceSKUsCachedReader")
	}
}

func requireControllerDependency(t *testing.T, controller Runnable, expected any, fields ...string) {
	t.Helper()
	field := reflect.ValueOf(controller)
	for _, name := range fields {
		for field.Kind() == reflect.Pointer || field.Kind() == reflect.Interface {
			field = field.Elem()
		}
		field = field.FieldByName(name)
		require.True(t, field.IsValid(), name)
	}
	for field.Kind() == reflect.Interface {
		field = field.Elem()
	}
	require.Equal(t, reflect.ValueOf(expected).Pointer(), field.Pointer(), strings.Join(fields, "."))
}

func TestControllerContextKeepsFactoriesNotIndividualInformers(t *testing.T) {
	contextType := reflect.TypeOf(ControllerContext{})
	for index := range contextType.NumField() {
		field := contextType.Field(index)
		require.False(t, strings.HasSuffix(field.Name, "Lister"), field.Name)
		require.False(t, strings.HasSuffix(field.Name, "Informer"), field.Name)
	}
	_, hasOldClientName := contextType.FieldByName("HTTPClient")
	require.False(t, hasOldClientName)
	for _, name := range []string{"BackendInformers", "FleetInformers", "UnionKubeApplierInformers", "AsyncOperationNotificationClient"} {
		_, exists := contextType.FieldByName(name)
		require.True(t, exists, name)
	}
}

func TestControllerRegistryNamedZoneRegistrations(t *testing.T) {
	files := token.NewFileSet()
	for zone, expectedCount := range map[string]int{
		"billing": 2, "cluster": 56, "clusterresources": 1, "cosmosmigration": 1,
		"datadump": 5, "externalauth": 10, "metrics": 6, "mismatch": 4, "nodepool": 19,
	} {
		source, err := parser.ParseFile(files, "../controllers/"+zone+"/registration.go", nil, 0)
		require.NoError(t, err)
		var builders, adapters, entries int
		for _, declaration := range source.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if strings.HasPrefix(function.Name.Name, "register") {
				builders++
			}
			if strings.HasPrefix(function.Name.Name, "instantiate") {
				adapters++
			}
			if function.Name.Name == "Register" {
				for _, statement := range function.Body.List {
					assignment := statement.(*ast.AssignStmt)
					key := assignment.Lhs[0].(*ast.IndexExpr).Index.(*ast.CallExpr)
					require.Equal(t, "ToLower", key.Fun.(*ast.SelectorExpr).Sel.Name)
					switch key.Args[0].(type) {
					case *ast.Ident, *ast.SelectorExpr:
					default:
						t.Fatalf("%s: registry key must reference a controller constant", zone)
					}
					entries++
				}
			}
		}
		require.Equal(t, expectedCount, builders, zone)
		require.Equal(t, expectedCount, adapters, zone)
		require.Equal(t, expectedCount, entries, zone)
	}
	source, err := os.ReadFile("controller_registry.go")
	require.NoError(t, err)
	require.NotContains(t, string(source), "controllerConstructionOrder")
	require.NotContains(t, string(source), "controllerLaunchOrder")
	require.NotContains(t, string(source), "const ")
}

type registryTestRunnable struct{}

func (*registryTestRunnable) Run(context.Context, int) {}

func TestControllerRegistryUnorderedConstructionAndErrors(t *testing.T) {
	registry := newControllerRegistry()
	var constructed []string
	for name, entry := range registry {
		entry.Instantiate = func(ControllerContext) (Runnable, error) {
			constructed = append(constructed, name)
			return &registryTestRunnable{}, nil
		}
		registry[name] = entry
	}
	_, err := instantiateControllers(registry, ControllerContext{HasRealFPA: true})
	require.NoError(t, err)
	require.ElementsMatch(t, slices.Collect(maps.Keys(registry)), constructed)
	constructed = nil
	_, err = instantiateControllers(registry, ControllerContext{})
	require.NoError(t, err)
	require.Len(t, constructed, 105)
	require.NotContains(t, constructed, "clusterdenyassignment")
	expectedErr := errors.New("constructor failed")
	name := "union-kube-applier-informers-controller"
	entry := registry[name]
	entry.Instantiate = func(ControllerContext) (Runnable, error) { return nil, expectedErr }
	registry[name] = entry
	controllers, err := instantiateControllers(registry, ControllerContext{})
	require.ErrorIs(t, err, expectedErr)
	require.ErrorContains(t, err, name)
	require.Nil(t, controllers)
}

type launchBackendInformers struct {
	coreinformers.BackendInformers
	started chan string
}

func (informers *launchBackendInformers) RunWithContext(ctx context.Context) {
	informers.started <- "backend"
	<-ctx.Done()
}

type launchFleetInformers struct {
	fleetinformers.FleetInformers
	started chan string
}

func (informers *launchFleetInformers) RunWithContext(ctx context.Context) {
	informers.started <- "fleet"
	<-ctx.Done()
}

func TestControllerRegistryStartsEveryProducerAndConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan string, 3)
	runnable := informerRunner{run: func(ctx context.Context) {
		started <- "consumer"
		<-ctx.Done()
	}}
	controllers := map[string]instantiatedController{"consumer": {runnable: runnable, workers: 20}}
	startControllers(ctx, controllers, ControllerContext{
		BackendInformers: &launchBackendInformers{started: started},
		FleetInformers:   &launchFleetInformers{started: started},
	})
	var names []string
	for range 3 {
		select {
		case name := <-started:
			names = append(names, name)
		case <-time.After(5 * time.Second):
			t.Fatal("launch blocked on a producer or consumer")
		}
	}
	require.ElementsMatch(t, []string{"backend", "fleet", "consumer"}, names)
}
