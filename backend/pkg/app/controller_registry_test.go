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
	"go/printer"
	"go/token"
	"net/http"
	"reflect"
	"runtime"
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
		require.NotNil(t, entry.instantiate, expected.name)
		function := runtime.FuncForPC(reflect.ValueOf(entry.instantiate).Pointer())
		require.Contains(t, function.Name(), ".instantiate", expected.name)
		require.NotContains(t, function.Name(), ".func", expected.name)
		if expected.name == "clusterdenyassignment" {
			require.NotNil(t, entry.Enabled)
			require.False(t, entry.Enabled(ControllerContext{}))
			require.True(t, entry.Enabled(ControllerContext{HasRealFPA: true}))
		} else {
			require.Nil(t, entry.Enabled, expected.name)
		}
	}
	require.Equal(t, expectedOrder, controllerLaunchOrder())
	require.ElementsMatch(t, expectedOrder, controllerConstructionOrder())
	for _, order := range [][]string{controllerLaunchOrder(), controllerConstructionOrder()} {
		seen := map[string]bool{}
		for _, name := range order {
			require.False(t, seen[name], "duplicate controller %s", name)
			seen[name] = true
		}
	}
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
			for index, controller := range controllers {
				require.Equal(t, expected[index].name, controller.name)
				require.Equal(t, expected[index].workers, controller.workers)
				require.NotNil(t, controller.runnable, controller.name)
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
	activeVersionsController, err := registry[controlPlaneActiveVersionsControllerName].Instantiate(controllerContext)
	require.NoError(t, err)
	requireControllerDependency(t, activeVersionsController, clusterLister, "syncer", "syncer", "clusterLister")
	requireControllerDependency(t, activeVersionsController, serviceProviderClusterLister, "syncer", "syncer", "serviceProviderClusterLister")
	requireControllerDependency(t, activeVersionsController, readLister, "syncer", "syncer", "readDesireLister")
	revocationController, err := registry[systemAdminCredentialRevocationDesiresControllerName].Instantiate(controllerContext)
	require.NoError(t, err)
	requireControllerDependency(t, revocationController, applyLister, "syncer", "syncer", "applyDesireLister")
	requireControllerDependency(t, revocationController, readLister, "syncer", "syncer", "readDesireLister")
	managementInformer, managementLister := controllerContext.FleetInformers.ManagementClusters()
	requireControllerDependency(t, unionController, managementInformer, "mcInformer")
	requireControllerDependency(t, unionController, managementLister, "mcLister")
	dumpController, err := registry[clusterRecursiveDataDumpControllerName].Instantiate(controllerContext)
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
	builders := map[string]bool{}
	constants := map[string]bool{}
	for zone, expectedCount := range map[string]int{
		"billing": 2, "cluster": 56, "clusterresources": 1, "cosmosmigration": 1,
		"datadump": 5, "externalauth": 10, "metrics": 6, "mismatch": 4,
		"nodepool": 19, "support": 2,
	} {
		source, err := parser.ParseFile(files, "controller_registry_"+zone+".go", nil, 0)
		require.NoError(t, err)
		var builderCount, adapterCount, constantCount int
		for _, declaration := range source.Decls {
			switch declaration := declaration.(type) {
			case *ast.FuncDecl:
				if strings.HasPrefix(declaration.Name.Name, "register") {
					builders[declaration.Name.Name] = true
					builderCount++
				}
				if strings.HasPrefix(declaration.Name.Name, "instantiate") {
					adapterCount++
				}
			case *ast.GenDecl:
				if declaration.Tok == token.CONST {
					for _, spec := range declaration.Specs {
						for _, name := range spec.(*ast.ValueSpec).Names {
							constants[name.Name] = true
							constantCount++
						}
					}
				}
			}
		}
		require.Equal(t, expectedCount, builderCount, zone)
		require.Equal(t, expectedCount, adapterCount, zone)
		require.Equal(t, expectedCount, constantCount, zone)
	}
	source, err := parser.ParseFile(files, "controller_registry.go", nil, 0)
	require.NoError(t, err)
	for _, declaration := range source.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch function.Name.Name {
		case "newControllerRegistry", "controllerConstructionOrder", "controllerLaunchOrder":
			returned := function.Body.List[0].(*ast.ReturnStmt).Results[0].(*ast.CompositeLit)
			require.Len(t, returned.Elts, 106, function.Name.Name)
			for _, element := range returned.Elts {
				if entry, ok := element.(*ast.KeyValueExpr); ok {
					call, ok := entry.Value.(*ast.CallExpr)
					require.True(t, ok, "registry entry must call a named builder")
					require.Empty(t, call.Args)
					require.True(t, builders[call.Fun.(*ast.Ident).Name])
					element = entry.Key
				}
				name, ok := element.(*ast.Ident)
				require.True(t, ok, "map and order lists must reference zone name constants")
				require.True(t, constants[name.Name], name.Name)
			}
		}
	}
}

type registryTestRunnable struct{}

func (*registryTestRunnable) Run(context.Context, int) {}

func TestControllerRegistryConstructionOrderAndErrors(t *testing.T) {
	registry := newControllerRegistry()
	var constructed []string
	for name, entry := range registry {
		entry.instantiate = func(ControllerContext) (Runnable, error) {
			constructed = append(constructed, name)
			return &registryTestRunnable{}, nil
		}
		registry[name] = entry
	}
	_, err := instantiateControllers(registry, ControllerContext{HasRealFPA: true})
	require.NoError(t, err)
	require.Equal(t, controllerConstructionOrder(), constructed)
	constructed = nil
	_, err = instantiateControllers(registry, ControllerContext{})
	require.NoError(t, err)
	require.Len(t, constructed, 105)
	require.NotContains(t, constructed, "clusterdenyassignment")
	expectedErr := errors.New("constructor failed")
	name := controllerConstructionOrder()[0]
	entry := registry[name]
	entry.instantiate = func(ControllerContext) (Runnable, error) { return nil, expectedErr }
	registry[name] = entry
	controllers, err := instantiateControllers(registry, ControllerContext{})
	require.ErrorIs(t, err, expectedErr)
	require.ErrorContains(t, err, name)
	require.Nil(t, controllers)
}

func TestControllerRegistryInformerLaunchOrder(t *testing.T) {
	files := token.NewFileSet()
	source, err := parser.ParseFile(files, "backend.go", nil, 0)
	require.NoError(t, err)
	var launches []string
	ast.Inspect(source, func(node ast.Node) bool {
		entry, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok := entry.Key.(*ast.Ident)
		if !ok || key.Name != "OnStartedLeading" {
			return true
		}
		ast.Inspect(entry.Value, func(node ast.Node) bool {
			launch, ok := node.(*ast.GoStmt)
			if ok {
				var expression strings.Builder
				require.NoError(t, printer.Fprint(&expression, files, launch.Call.Fun))
				launches = append(launches, expression.String())
			}
			return true
		})
		return false
	})
	require.Equal(t, []string{
		"controllerContext.BackendInformers.RunWithContext",
		"controllerContext.FleetInformers.RunWithContext",
		"controller.runnable.Run",
	}, launches)
	require.Equal(t, "union-kube-applier-informers-controller", controllerLaunchOrder()[0])
}
