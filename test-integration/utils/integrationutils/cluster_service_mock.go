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

package integrationutils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dario.cat/mergo"
	"go.uber.org/mock/gomock"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/sets"

	csarhcpv1alpha1 "github.com/openshift-online/ocm-api-model/clientapi/arohcp/v1alpha1"
	arohcpv1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	ocmerrors "github.com/openshift-online/ocm-sdk-go/errors"

	"github.com/Azure/ARO-HCP/internal/mocks"
	"github.com/Azure/ARO-HCP/internal/ocm"
)

type ClusterServiceMock struct {
	ArtifactsDir string
	mockData     map[string]map[string][]any

	MockClusterServiceClient *mocks.MockClusterServiceClientSpec
}

func NewClusterServiceMock(t *testing.T, artifactsDir string) *ClusterServiceMock {
	ctrl := gomock.NewController(t)
	clusterServiceClient := mocks.NewMockClusterServiceClientSpec(ctrl)

	ret := &ClusterServiceMock{
		ArtifactsDir:             artifactsDir,
		mockData:                 map[string]map[string][]any{},
		MockClusterServiceClient: clusterServiceClient,
	}
	ret.setupMockClusterService(t)
	return ret
}

func (s *ClusterServiceMock) setupMockClusterService(t *testing.T) {
	internalIDToCluster := s.GetOrCreateMockData(t.Name() + "_clusters")
	internalIDToExternalAuth := s.GetOrCreateMockData(t.Name() + "_externalAuths")
	internalIDToNodePool := s.GetOrCreateMockData(t.Name() + "_nodePools")
	internalIDToAutoscaler := s.GetOrCreateMockData(t.Name() + "_autoscalers")

	s.MockClusterServiceClient.EXPECT().PostCluster(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, clusterBuilder *csarhcpv1alpha1.ClusterBuilder, autoscalerBuilder *csarhcpv1alpha1.ClusterAutoscalerBuilder) (*csarhcpv1alpha1.Cluster, error) {
		justID := rand.String(10)
		internalID := "/api/clusters_mgmt/v1/clusters/" + justID

		if autoscalerBuilder != nil {
			autoscaler, err := autoscalerBuilder.HREF(internalID).Build()
			if err != nil {
				return nil, err
			}

			internalIDToAutoscaler[internalID] = append(internalIDToAutoscaler[internalID], autoscaler)
		}

		ret, err := clusterBuilder.ID(justID).HREF(internalID).Build()
		if err != nil {
			return nil, err
		}

		// these values are normally looked up directly from azure inside of cluster-service.  For mocks we do it here.
		ret, err = addFakeAzureIdentityData(ret)
		if err != nil {
			return nil, err
		}

		internalIDToCluster[internalID] = append(internalIDToCluster[internalID], ret)
		return ret, nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().UpdateCluster(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID, builder *arohcpv1alpha1.ClusterBuilder) (*arohcpv1alpha1.Cluster, error) {
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToCluster[id.String()] = append(internalIDToCluster[id.String()], ret)
		return ret, nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().DeleteCluster(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) error {
		_, exists := internalIDToCluster[id.String()]
		if !exists {
			return fmt.Errorf("cluster %q does not exist", id.String())
		}
		delete(internalIDToCluster, id.String())
		delete(internalIDToAutoscaler, id.String())

		return nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().UpdateClusterAutoscaler(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, internalID ocm.InternalID, builder *arohcpv1alpha1.ClusterAutoscalerBuilder) (*arohcpv1alpha1.ClusterAutoscaler, error) {
		ret, err := builder.HREF(internalID.String()).Build()
		if err != nil {
			return nil, err
		}

		internalIDToAutoscaler[internalID.String()] = append(internalIDToAutoscaler[internalID.String()], ret)
		return ret, nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().GetCluster(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) (*csarhcpv1alpha1.Cluster, error) {
		ret, err := mergeClusterServiceClusterAndAutoscaler(internalIDToCluster[id.String()], internalIDToAutoscaler[id.String()])
		if err != nil {
			return nil, fmt.Errorf("failed to merge cluster id %q: %w", id.String(), err)
		}
		return ret, nil

	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().ListClusters(gomock.Any()).DoAndReturn(func(s string) ocm.ClusterListIterator {
		allObjs := []*csarhcpv1alpha1.Cluster{}
		for _, key := range sets.StringKeySet(internalIDToCluster).List() {
			obj, err := mergeClusterServiceClusterAndAutoscaler(internalIDToCluster[key], internalIDToAutoscaler[key])
			if err != nil {
				panic(fmt.Errorf("failed to merge cluster id %q: %w", key, err))
			}
			allObjs = append(allObjs, obj)
		}
		return ocm.NewSimpleClusterListIterator(allObjs, nil)
	}).AnyTimes()

	s.MockClusterServiceClient.EXPECT().PostExternalAuth(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, clusterID ocm.InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
		justID := rand.String(10)
		builder.ID(justID)
		externalAuthInternalID := clusterID.String() + "/external_auth_config/external_auths/" + justID
		builder = builder.HREF(externalAuthInternalID)
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToExternalAuth[externalAuthInternalID] = append(internalIDToExternalAuth[externalAuthInternalID], ret)
		return ret, nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().UpdateExternalAuth(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID, builder *arohcpv1alpha1.ExternalAuthBuilder) (*arohcpv1alpha1.ExternalAuth, error) {
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToExternalAuth[id.String()] = append(internalIDToExternalAuth[id.String()], ret)
		return ret, nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().GetExternalAuth(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) (*arohcpv1alpha1.ExternalAuth, error) {
		ret, err := mergeClusterServiceInstance[csarhcpv1alpha1.ExternalAuth](internalIDToExternalAuth[id.String()])
		if err != nil {
			return nil, fmt.Errorf("failed to merge external auth id %q: %w", id.String(), err)
		}
		return ret, nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().ListExternalAuths(gomock.Any(), gomock.Any()).DoAndReturn(func(id ocm.InternalID, s string) ocm.ExternalAuthListIterator {
		clusterIDString := id.String()
		allObjs := []*csarhcpv1alpha1.ExternalAuth{}
		for _, key := range sets.StringKeySet(internalIDToExternalAuth).List() {
			if !strings.Contains(key, clusterIDString) {
				// only include for the right cluster
				continue
			}
			obj, err := mergeClusterServiceInstance[csarhcpv1alpha1.ExternalAuth](internalIDToExternalAuth[key])
			if err != nil {
				panic(fmt.Errorf("failed to merge external auth id %q: %w", key, err))
			}
			allObjs = append(allObjs, obj)
		}
		return ocm.NewSimpleExternalAuthListIterator(allObjs, nil)
	}).AnyTimes()

	s.MockClusterServiceClient.EXPECT().PostNodePool(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, clusterID ocm.InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
		justID := rand.String(10)
		nodePoolInternalID := clusterID.String() + "/node_pools/" + justID

		ret, err := builder.ID(justID).HREF(nodePoolInternalID).Build()
		if err != nil {
			return nil, err
		}

		internalIDToNodePool[nodePoolInternalID] = append(internalIDToNodePool[nodePoolInternalID], ret)
		return ret, nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().UpdateNodePool(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID, builder *arohcpv1alpha1.NodePoolBuilder) (*arohcpv1alpha1.NodePool, error) {
		ret, err := builder.Build()
		if err != nil {
			return nil, err
		}

		internalIDToNodePool[id.String()] = append(internalIDToNodePool[id.String()], ret)
		return ret, nil
	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().GetNodePool(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, id ocm.InternalID) (*arohcpv1alpha1.NodePool, error) {
		ret, err := mergeClusterServiceInstance[csarhcpv1alpha1.NodePool](internalIDToNodePool[id.String()])
		if err != nil {
			return nil, fmt.Errorf("failed to merge nodepool id %q: %w", id.String(), err)
		}
		return ret, nil

	}).AnyTimes()
	s.MockClusterServiceClient.EXPECT().ListNodePools(gomock.Any(), gomock.Any()).DoAndReturn(func(id ocm.InternalID, s string) ocm.NodePoolListIterator {
		clusterIDString := id.String()
		allObjs := []*csarhcpv1alpha1.NodePool{}
		for _, key := range sets.StringKeySet(internalIDToNodePool).List() {
			if !strings.Contains(key, clusterIDString) {
				// only include for the right cluster
				continue
			}
			obj, err := mergeClusterServiceInstance[csarhcpv1alpha1.NodePool](internalIDToNodePool[key])
			if err != nil {
				panic(fmt.Errorf("failed to merge nodepool id %q: %w", key, err))
			}
			allObjs = append(allObjs, obj)
		}
		return ocm.NewSimpleNodePoolListIterator(allObjs, nil)
	}).AnyTimes()
}

func (s *ClusterServiceMock) AddContent(t *testing.T, initialDataDir fs.FS) error {
	internalIDToCluster := s.GetOrCreateMockData(t.Name() + "_clusters")
	internalIDToExternalAuth := s.GetOrCreateMockData(t.Name() + "_externalAuths")
	internalIDToNodePool := s.GetOrCreateMockData(t.Name() + "_nodePools")
	internalIDToAutoscaler := s.GetOrCreateMockData(t.Name() + "_autoscalers")

	dirContent, err := fs.ReadDir(initialDataDir, ".")
	if err != nil {
		return fmt.Errorf("failed to read dir: %w", err)
	}

	for _, dirEntry := range dirContent {
		if dirEntry.IsDir() {
			return fmt.Errorf("dir %s is not a file", dirEntry.Name())
		}
		fileReader, err := initialDataDir.Open(dirEntry.Name())
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", dirEntry.Name(), err)
		}
		fileContent, err := io.ReadAll(fileReader)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", dirEntry.Name(), err)
		}

		switch {
		case strings.HasSuffix(dirEntry.Name(), "-cluster.json"):
			obj, err := arohcpv1alpha1.UnmarshalCluster(fileContent)
			if err != nil {
				return fmt.Errorf("failed to unmarshal cluster: %w", err)
			}
			if _, exists := internalIDToCluster[obj.HREF()]; exists {
				return fmt.Errorf("duplicate cluster: %s", obj.HREF())
			}
			internalIDToCluster[obj.HREF()] = []any{obj}

		case strings.HasSuffix(dirEntry.Name(), "-externalauth.json"):
			obj, err := arohcpv1alpha1.UnmarshalExternalAuth(fileContent)
			if err != nil {
				return fmt.Errorf("failed to unmarshal nodepool: %w", err)
			}
			if _, exists := internalIDToExternalAuth[obj.HREF()]; exists {
				return fmt.Errorf("duplicate nodepool: %s", obj.HREF())
			}
			internalIDToExternalAuth[obj.HREF()] = []any{obj}

		case strings.HasSuffix(dirEntry.Name(), "-nodepool.json"):
			obj, err := arohcpv1alpha1.UnmarshalNodePool(fileContent)
			if err != nil {
				return fmt.Errorf("failed to unmarshal nodepool: %w", err)
			}
			if _, exists := internalIDToNodePool[obj.HREF()]; exists {
				return fmt.Errorf("duplicate nodepool: %s", obj.HREF())
			}
			internalIDToNodePool[obj.HREF()] = []any{obj}

		case strings.HasSuffix(dirEntry.Name(), "-autoscaler.json"):
			obj, err := arohcpv1alpha1.UnmarshalClusterAutoscaler(fileContent)
			if err != nil {
				return fmt.Errorf("failed to unmarshal cluster: %w", err)
			}
			if _, exists := internalIDToAutoscaler[obj.HREF()]; exists {
				return fmt.Errorf("duplicate autoscaler: %s", obj.HREF())
			}
			internalIDToAutoscaler[obj.HREF()] = []any{obj}

		default:
			return fmt.Errorf("unknown file %s", dirEntry.Name())
		}
	}

	return nil
}

func addFakeAzureIdentityData(clusterServiceCluster any) (*csarhcpv1alpha1.Cluster, error) {
	// the API is so hard to work with that we'll make it a map[string]any to manipulate it
	inJSON, err := marshalClusterServiceAny(clusterServiceCluster)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cluster-service type: %w", err)
	}
	content := map[string]any{}
	if err := json.Unmarshal(inJSON, &content); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cluster-service type: %w", err)
	}

	controlPlaneManagedIdentities, _, _ := unstructured.NestedMap(content, "azure", "operators_authentication", "managed_identities", "control_plane_operators_managed_identities")
	if len(controlPlaneManagedIdentities) > 0 {
		for key, clusterServiceManagedIdentityInfo := range controlPlaneManagedIdentities {
			setFakeAzureIdentityFields(key, clusterServiceManagedIdentityInfo)
		}
		err := unstructured.SetNestedMap(content, controlPlaneManagedIdentities, "azure", "operators_authentication", "managed_identities", "control_plane_operators_managed_identities")
		if err != nil {
			return nil, fmt.Errorf("failed to set nested map: %w", err)
		}
	}

	dataPlaneManagedIdentities, _, _ := unstructured.NestedMap(content, "azure", "operators_authentication", "managed_identities", "data_plane_operators_managed_identities")
	if len(dataPlaneManagedIdentities) > 0 {
		for key, clusterServiceManagedIdentityInfo := range dataPlaneManagedIdentities {
			setFakeAzureIdentityFields(key, clusterServiceManagedIdentityInfo)
		}
		err := unstructured.SetNestedMap(content, dataPlaneManagedIdentities, "azure", "operators_authentication", "managed_identities", "data_plane_operators_managed_identities")
		if err != nil {
			return nil, fmt.Errorf("failed to set nested map: %w", err)
		}
	}

	serviceManagedIdentity, _, _ := unstructured.NestedMap(content, "azure", "operators_authentication", "managed_identities", "service_managed_identity")
	if serviceManagedIdentity != nil {
		setFakeAzureIdentityFields("service-managed-identity", serviceManagedIdentity)
		err = unstructured.SetNestedMap(content, serviceManagedIdentity, "azure", "operators_authentication", "managed_identities", "service_managed_identity")
		if err != nil {
			return nil, fmt.Errorf("failed to set nested map: %w", err)
		}
	}

	outJSON, err := json.MarshalIndent(content, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cluster-service type: %w", err)
	}
	return csarhcpv1alpha1.UnmarshalCluster(outJSON)
}

func setFakeAzureIdentityFields(key string, uncastManagedIdentity any) any {
	managedIdentity := uncastManagedIdentity.(map[string]any)
	managedIdentity["client_id"] = key + "_fake-client-id"
	managedIdentity["principal_id"] = key + "_fake-principal-id"
	return managedIdentity
}

func (s *ClusterServiceMock) Cleanup(ctx context.Context) {
	if err := s.saveClusterServiceMockData(ctx); err != nil {
		fmt.Printf("Failed to save mock data: %v\n", err)
	}
}

func (s *ClusterServiceMock) saveClusterServiceMockData(ctx context.Context) error {
	for dataName, clusterServiceData := range s.mockData {
		for clusterServiceName, clusterServiceHistory := range clusterServiceData {
			for i, currCluster := range clusterServiceHistory {
				basename := fmt.Sprintf("%d_%s.json", i, strings.ReplaceAll(clusterServiceName, "/", "."))
				filename := filepath.Join(s.ArtifactsDir, "cluster-service-mock-data", dataName, strings.ReplaceAll(clusterServiceName, "/", "."), basename)
				dirname := filepath.Dir(filename)
				if err := os.MkdirAll(dirname, 0755); err != nil {
					return fmt.Errorf("failed to create directory %s: %w", dirname, err)
				}

				clusterServiceBytes, err := marshalClusterServiceAny(currCluster)
				if err != nil {
					return fmt.Errorf("failed to marshal cluster: %w", err)
				}
				obj := map[string]any{}
				if err := json.Unmarshal(clusterServiceBytes, &obj); err != nil {
					return fmt.Errorf("failed to unmarshal cluster: %w", err)
				}
				prettyPrint, err := json.MarshalIndent(obj, "", "    ")
				if err != nil {
					return fmt.Errorf("failed to marshal document: %w", err)
				}
				if err := os.WriteFile(filename, prettyPrint, 0644); err != nil {
					return fmt.Errorf("failed to write document to %s: %w", filename, err)
				}
			}
		}
	}

	return nil
}

func (s *ClusterServiceMock) GetOrCreateMockData(dataName string) map[string][]any {
	if existing, ok := s.mockData[dataName]; ok {
		return existing
	}
	newData := map[string][]any{}
	s.mockData[dataName] = newData
	return newData
}

func mergeClusterServiceReturn(history []any) ([]byte, error) {
	if len(history) == 0 {
		retErr, err := ocmerrors.NewError().Status(http.StatusNotFound).Build()
		if err != nil {
			panic(err)
		}
		return nil, retErr
	}
	// this looks insane, but cluster-service has some of the toughest API and client constructs to manage.
	// we need to merge the history together, but the CS types resist that, so taking it all back to maps is easier.
	dest := map[string]any{}
	for _, curr := range history {
		clusterServiceJSON, err := marshalClusterServiceAny(curr)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal cluster-service type: %w", err)
		}
		currMap := map[string]any{}
		if err := json.Unmarshal(clusterServiceJSON, &currMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal cluster-service type: %w", err)
		}
		if err := mergo.Merge(&dest, currMap, mergo.WithOverride); err != nil {
			return nil, fmt.Errorf("failed to merge cluster-service type: %w", err)
		}
	}
	return json.Marshal(dest)
}

func mergeClusterServiceInstance[T any](history []any) (*T, error) {
	mergedJSON, err := mergeClusterServiceReturn(history)
	if err != nil {
		return nil, err
	}

	return unmarshalClusterServiceAny[T](mergedJSON)
}

func mergeClusterServiceClusterAndAutoscaler(clusterHistory []any, autoscalerHistory []any) (*arohcpv1alpha1.Cluster, error) {
	cluster, err := mergeClusterServiceInstance[csarhcpv1alpha1.Cluster](clusterHistory)
	if err != nil {
		return nil, err
	}

	clusterBuilder := csarhcpv1alpha1.NewCluster().Copy(cluster)

	if len(autoscalerHistory) > 0 {
		autoscaler, err := mergeClusterServiceInstance[csarhcpv1alpha1.ClusterAutoscaler](autoscalerHistory)
		if err != nil {
			return nil, err
		}

		clusterBuilder.Autoscaler(csarhcpv1alpha1.NewClusterAutoscaler().Copy(autoscaler))
	}

	return clusterBuilder.Build()
}

func unmarshalClusterServiceAny[T any](mergedJSON []byte) (*T, error) {
	var obj T
	switch any(&obj).(type) {
	case *csarhcpv1alpha1.Cluster:
		ret, err := csarhcpv1alpha1.UnmarshalCluster(mergedJSON)
		if err != nil {
			return nil, err
		}
		return any(ret).(*T), err
	case *csarhcpv1alpha1.ClusterAutoscaler:
		ret, err := csarhcpv1alpha1.UnmarshalClusterAutoscaler(mergedJSON)
		if err != nil {
			return nil, err
		}
		return any(ret).(*T), err
	case *csarhcpv1alpha1.NodePool:
		ret, err := csarhcpv1alpha1.UnmarshalNodePool(mergedJSON)
		if err != nil {
			return nil, err
		}
		return any(ret).(*T), err
	case *csarhcpv1alpha1.ExternalAuth:
		ret, err := csarhcpv1alpha1.UnmarshalExternalAuth(mergedJSON)
		if err != nil {
			return nil, err
		}
		return any(ret).(*T), err
	default:
		return nil, fmt.Errorf("unknown type: %T", &obj)
	}
}

// cluster service types fight the standard golang stack and don't conform to standard json interfaces.
func marshalClusterServiceAny(clusterServiceData any) ([]byte, error) {
	switch castObj := clusterServiceData.(type) {
	case *csarhcpv1alpha1.Cluster:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalCluster(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *csarhcpv1alpha1.ClusterAutoscaler:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalClusterAutoscaler(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *csarhcpv1alpha1.ExternalAuth:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalExternalAuth(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case *csarhcpv1alpha1.NodePool:
		buf := &bytes.Buffer{}
		if err := csarhcpv1alpha1.MarshalNodePool(castObj, buf); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, fmt.Errorf("unknown type: %T", castObj)
	}
}
