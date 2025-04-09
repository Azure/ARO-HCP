// Code generated by MockGen. DO NOT EDIT.
// Source: ../ocm/ocm.go
//
// Generated by this command:
//
//	mockgen -typed -source=../ocm/ocm.go -destination=ocm.go -package mocks github.com/Azure/ARO-HCP/internal/ocm ClusterServiceClientSpec
//

// Package mocks is a generated GoMock package.
package mocks

import (
	context "context"
	reflect "reflect"

	ocm "github.com/Azure/ARO-HCP/internal/ocm"
	v1alpha1 "github.com/openshift-online/ocm-sdk-go/arohcp/v1alpha1"
	v1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	gomock "go.uber.org/mock/gomock"
)

// MockClusterServiceClientSpec is a mock of ClusterServiceClientSpec interface.
type MockClusterServiceClientSpec struct {
	ctrl     *gomock.Controller
	recorder *MockClusterServiceClientSpecMockRecorder
	isgomock struct{}
}

// MockClusterServiceClientSpecMockRecorder is the mock recorder for MockClusterServiceClientSpec.
type MockClusterServiceClientSpecMockRecorder struct {
	mock *MockClusterServiceClientSpec
}

// NewMockClusterServiceClientSpec creates a new mock instance.
func NewMockClusterServiceClientSpec(ctrl *gomock.Controller) *MockClusterServiceClientSpec {
	mock := &MockClusterServiceClientSpec{ctrl: ctrl}
	mock.recorder = &MockClusterServiceClientSpecMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockClusterServiceClientSpec) EXPECT() *MockClusterServiceClientSpecMockRecorder {
	return m.recorder
}

// AddProperties mocks base method.
func (m *MockClusterServiceClientSpec) AddProperties(builder *v1alpha1.ClusterBuilder) *v1alpha1.ClusterBuilder {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AddProperties", builder)
	ret0, _ := ret[0].(*v1alpha1.ClusterBuilder)
	return ret0
}

// AddProperties indicates an expected call of AddProperties.
func (mr *MockClusterServiceClientSpecMockRecorder) AddProperties(builder any) *MockClusterServiceClientSpecAddPropertiesCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AddProperties", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).AddProperties), builder)
	return &MockClusterServiceClientSpecAddPropertiesCall{Call: call}
}

// MockClusterServiceClientSpecAddPropertiesCall wrap *gomock.Call
type MockClusterServiceClientSpecAddPropertiesCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecAddPropertiesCall) Return(arg0 *v1alpha1.ClusterBuilder) *MockClusterServiceClientSpecAddPropertiesCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecAddPropertiesCall) Do(f func(*v1alpha1.ClusterBuilder) *v1alpha1.ClusterBuilder) *MockClusterServiceClientSpecAddPropertiesCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecAddPropertiesCall) DoAndReturn(f func(*v1alpha1.ClusterBuilder) *v1alpha1.ClusterBuilder) *MockClusterServiceClientSpecAddPropertiesCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// DeleteBreakGlassCredentials mocks base method.
func (m *MockClusterServiceClientSpec) DeleteBreakGlassCredentials(ctx context.Context, clusterInternalID ocm.InternalID) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteBreakGlassCredentials", ctx, clusterInternalID)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeleteBreakGlassCredentials indicates an expected call of DeleteBreakGlassCredentials.
func (mr *MockClusterServiceClientSpecMockRecorder) DeleteBreakGlassCredentials(ctx, clusterInternalID any) *MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteBreakGlassCredentials", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).DeleteBreakGlassCredentials), ctx, clusterInternalID)
	return &MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall{Call: call}
}

// MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall wrap *gomock.Call
type MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall) Return(arg0 error) *MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall) Do(f func(context.Context, ocm.InternalID) error) *MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall) DoAndReturn(f func(context.Context, ocm.InternalID) error) *MockClusterServiceClientSpecDeleteBreakGlassCredentialsCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// DeleteCluster mocks base method.
func (m *MockClusterServiceClientSpec) DeleteCluster(ctx context.Context, internalID ocm.InternalID) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteCluster", ctx, internalID)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeleteCluster indicates an expected call of DeleteCluster.
func (mr *MockClusterServiceClientSpecMockRecorder) DeleteCluster(ctx, internalID any) *MockClusterServiceClientSpecDeleteClusterCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteCluster", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).DeleteCluster), ctx, internalID)
	return &MockClusterServiceClientSpecDeleteClusterCall{Call: call}
}

// MockClusterServiceClientSpecDeleteClusterCall wrap *gomock.Call
type MockClusterServiceClientSpecDeleteClusterCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecDeleteClusterCall) Return(arg0 error) *MockClusterServiceClientSpecDeleteClusterCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecDeleteClusterCall) Do(f func(context.Context, ocm.InternalID) error) *MockClusterServiceClientSpecDeleteClusterCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecDeleteClusterCall) DoAndReturn(f func(context.Context, ocm.InternalID) error) *MockClusterServiceClientSpecDeleteClusterCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// DeleteNodePool mocks base method.
func (m *MockClusterServiceClientSpec) DeleteNodePool(ctx context.Context, internalID ocm.InternalID) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteNodePool", ctx, internalID)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeleteNodePool indicates an expected call of DeleteNodePool.
func (mr *MockClusterServiceClientSpecMockRecorder) DeleteNodePool(ctx, internalID any) *MockClusterServiceClientSpecDeleteNodePoolCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteNodePool", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).DeleteNodePool), ctx, internalID)
	return &MockClusterServiceClientSpecDeleteNodePoolCall{Call: call}
}

// MockClusterServiceClientSpecDeleteNodePoolCall wrap *gomock.Call
type MockClusterServiceClientSpecDeleteNodePoolCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecDeleteNodePoolCall) Return(arg0 error) *MockClusterServiceClientSpecDeleteNodePoolCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecDeleteNodePoolCall) Do(f func(context.Context, ocm.InternalID) error) *MockClusterServiceClientSpecDeleteNodePoolCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecDeleteNodePoolCall) DoAndReturn(f func(context.Context, ocm.InternalID) error) *MockClusterServiceClientSpecDeleteNodePoolCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// GetBreakGlassCredential mocks base method.
func (m *MockClusterServiceClientSpec) GetBreakGlassCredential(ctx context.Context, internalID ocm.InternalID) (*v1.BreakGlassCredential, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetBreakGlassCredential", ctx, internalID)
	ret0, _ := ret[0].(*v1.BreakGlassCredential)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetBreakGlassCredential indicates an expected call of GetBreakGlassCredential.
func (mr *MockClusterServiceClientSpecMockRecorder) GetBreakGlassCredential(ctx, internalID any) *MockClusterServiceClientSpecGetBreakGlassCredentialCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetBreakGlassCredential", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).GetBreakGlassCredential), ctx, internalID)
	return &MockClusterServiceClientSpecGetBreakGlassCredentialCall{Call: call}
}

// MockClusterServiceClientSpecGetBreakGlassCredentialCall wrap *gomock.Call
type MockClusterServiceClientSpecGetBreakGlassCredentialCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecGetBreakGlassCredentialCall) Return(arg0 *v1.BreakGlassCredential, arg1 error) *MockClusterServiceClientSpecGetBreakGlassCredentialCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecGetBreakGlassCredentialCall) Do(f func(context.Context, ocm.InternalID) (*v1.BreakGlassCredential, error)) *MockClusterServiceClientSpecGetBreakGlassCredentialCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecGetBreakGlassCredentialCall) DoAndReturn(f func(context.Context, ocm.InternalID) (*v1.BreakGlassCredential, error)) *MockClusterServiceClientSpecGetBreakGlassCredentialCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// GetCluster mocks base method.
func (m *MockClusterServiceClientSpec) GetCluster(ctx context.Context, internalID ocm.InternalID) (*v1alpha1.Cluster, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetCluster", ctx, internalID)
	ret0, _ := ret[0].(*v1alpha1.Cluster)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetCluster indicates an expected call of GetCluster.
func (mr *MockClusterServiceClientSpecMockRecorder) GetCluster(ctx, internalID any) *MockClusterServiceClientSpecGetClusterCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetCluster", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).GetCluster), ctx, internalID)
	return &MockClusterServiceClientSpecGetClusterCall{Call: call}
}

// MockClusterServiceClientSpecGetClusterCall wrap *gomock.Call
type MockClusterServiceClientSpecGetClusterCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecGetClusterCall) Return(arg0 *v1alpha1.Cluster, arg1 error) *MockClusterServiceClientSpecGetClusterCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecGetClusterCall) Do(f func(context.Context, ocm.InternalID) (*v1alpha1.Cluster, error)) *MockClusterServiceClientSpecGetClusterCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecGetClusterCall) DoAndReturn(f func(context.Context, ocm.InternalID) (*v1alpha1.Cluster, error)) *MockClusterServiceClientSpecGetClusterCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// GetClusterStatus mocks base method.
func (m *MockClusterServiceClientSpec) GetClusterStatus(ctx context.Context, internalID ocm.InternalID) (*v1alpha1.ClusterStatus, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetClusterStatus", ctx, internalID)
	ret0, _ := ret[0].(*v1alpha1.ClusterStatus)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetClusterStatus indicates an expected call of GetClusterStatus.
func (mr *MockClusterServiceClientSpecMockRecorder) GetClusterStatus(ctx, internalID any) *MockClusterServiceClientSpecGetClusterStatusCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetClusterStatus", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).GetClusterStatus), ctx, internalID)
	return &MockClusterServiceClientSpecGetClusterStatusCall{Call: call}
}

// MockClusterServiceClientSpecGetClusterStatusCall wrap *gomock.Call
type MockClusterServiceClientSpecGetClusterStatusCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecGetClusterStatusCall) Return(arg0 *v1alpha1.ClusterStatus, arg1 error) *MockClusterServiceClientSpecGetClusterStatusCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecGetClusterStatusCall) Do(f func(context.Context, ocm.InternalID) (*v1alpha1.ClusterStatus, error)) *MockClusterServiceClientSpecGetClusterStatusCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecGetClusterStatusCall) DoAndReturn(f func(context.Context, ocm.InternalID) (*v1alpha1.ClusterStatus, error)) *MockClusterServiceClientSpecGetClusterStatusCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// GetNodePool mocks base method.
func (m *MockClusterServiceClientSpec) GetNodePool(ctx context.Context, internalID ocm.InternalID) (*v1alpha1.NodePool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNodePool", ctx, internalID)
	ret0, _ := ret[0].(*v1alpha1.NodePool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetNodePool indicates an expected call of GetNodePool.
func (mr *MockClusterServiceClientSpecMockRecorder) GetNodePool(ctx, internalID any) *MockClusterServiceClientSpecGetNodePoolCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNodePool", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).GetNodePool), ctx, internalID)
	return &MockClusterServiceClientSpecGetNodePoolCall{Call: call}
}

// MockClusterServiceClientSpecGetNodePoolCall wrap *gomock.Call
type MockClusterServiceClientSpecGetNodePoolCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecGetNodePoolCall) Return(arg0 *v1alpha1.NodePool, arg1 error) *MockClusterServiceClientSpecGetNodePoolCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecGetNodePoolCall) Do(f func(context.Context, ocm.InternalID) (*v1alpha1.NodePool, error)) *MockClusterServiceClientSpecGetNodePoolCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecGetNodePoolCall) DoAndReturn(f func(context.Context, ocm.InternalID) (*v1alpha1.NodePool, error)) *MockClusterServiceClientSpecGetNodePoolCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// GetNodePoolStatus mocks base method.
func (m *MockClusterServiceClientSpec) GetNodePoolStatus(ctx context.Context, internalID ocm.InternalID) (*v1alpha1.NodePoolStatus, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNodePoolStatus", ctx, internalID)
	ret0, _ := ret[0].(*v1alpha1.NodePoolStatus)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetNodePoolStatus indicates an expected call of GetNodePoolStatus.
func (mr *MockClusterServiceClientSpecMockRecorder) GetNodePoolStatus(ctx, internalID any) *MockClusterServiceClientSpecGetNodePoolStatusCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNodePoolStatus", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).GetNodePoolStatus), ctx, internalID)
	return &MockClusterServiceClientSpecGetNodePoolStatusCall{Call: call}
}

// MockClusterServiceClientSpecGetNodePoolStatusCall wrap *gomock.Call
type MockClusterServiceClientSpecGetNodePoolStatusCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecGetNodePoolStatusCall) Return(arg0 *v1alpha1.NodePoolStatus, arg1 error) *MockClusterServiceClientSpecGetNodePoolStatusCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecGetNodePoolStatusCall) Do(f func(context.Context, ocm.InternalID) (*v1alpha1.NodePoolStatus, error)) *MockClusterServiceClientSpecGetNodePoolStatusCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecGetNodePoolStatusCall) DoAndReturn(f func(context.Context, ocm.InternalID) (*v1alpha1.NodePoolStatus, error)) *MockClusterServiceClientSpecGetNodePoolStatusCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// ListBreakGlassCredentials mocks base method.
func (m *MockClusterServiceClientSpec) ListBreakGlassCredentials(clusterInternalID ocm.InternalID, searchExpression string) ocm.BreakGlassCredentialListIterator {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListBreakGlassCredentials", clusterInternalID, searchExpression)
	ret0, _ := ret[0].(ocm.BreakGlassCredentialListIterator)
	return ret0
}

// ListBreakGlassCredentials indicates an expected call of ListBreakGlassCredentials.
func (mr *MockClusterServiceClientSpecMockRecorder) ListBreakGlassCredentials(clusterInternalID, searchExpression any) *MockClusterServiceClientSpecListBreakGlassCredentialsCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListBreakGlassCredentials", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).ListBreakGlassCredentials), clusterInternalID, searchExpression)
	return &MockClusterServiceClientSpecListBreakGlassCredentialsCall{Call: call}
}

// MockClusterServiceClientSpecListBreakGlassCredentialsCall wrap *gomock.Call
type MockClusterServiceClientSpecListBreakGlassCredentialsCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecListBreakGlassCredentialsCall) Return(arg0 ocm.BreakGlassCredentialListIterator) *MockClusterServiceClientSpecListBreakGlassCredentialsCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecListBreakGlassCredentialsCall) Do(f func(ocm.InternalID, string) ocm.BreakGlassCredentialListIterator) *MockClusterServiceClientSpecListBreakGlassCredentialsCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecListBreakGlassCredentialsCall) DoAndReturn(f func(ocm.InternalID, string) ocm.BreakGlassCredentialListIterator) *MockClusterServiceClientSpecListBreakGlassCredentialsCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// ListClusters mocks base method.
func (m *MockClusterServiceClientSpec) ListClusters(searchExpression string) ocm.ClusterListIterator {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListClusters", searchExpression)
	ret0, _ := ret[0].(ocm.ClusterListIterator)
	return ret0
}

// ListClusters indicates an expected call of ListClusters.
func (mr *MockClusterServiceClientSpecMockRecorder) ListClusters(searchExpression any) *MockClusterServiceClientSpecListClustersCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListClusters", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).ListClusters), searchExpression)
	return &MockClusterServiceClientSpecListClustersCall{Call: call}
}

// MockClusterServiceClientSpecListClustersCall wrap *gomock.Call
type MockClusterServiceClientSpecListClustersCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecListClustersCall) Return(arg0 ocm.ClusterListIterator) *MockClusterServiceClientSpecListClustersCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecListClustersCall) Do(f func(string) ocm.ClusterListIterator) *MockClusterServiceClientSpecListClustersCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecListClustersCall) DoAndReturn(f func(string) ocm.ClusterListIterator) *MockClusterServiceClientSpecListClustersCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// ListNodePools mocks base method.
func (m *MockClusterServiceClientSpec) ListNodePools(clusterInternalID ocm.InternalID, searchExpression string) ocm.NodePoolListIterator {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListNodePools", clusterInternalID, searchExpression)
	ret0, _ := ret[0].(ocm.NodePoolListIterator)
	return ret0
}

// ListNodePools indicates an expected call of ListNodePools.
func (mr *MockClusterServiceClientSpecMockRecorder) ListNodePools(clusterInternalID, searchExpression any) *MockClusterServiceClientSpecListNodePoolsCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListNodePools", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).ListNodePools), clusterInternalID, searchExpression)
	return &MockClusterServiceClientSpecListNodePoolsCall{Call: call}
}

// MockClusterServiceClientSpecListNodePoolsCall wrap *gomock.Call
type MockClusterServiceClientSpecListNodePoolsCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecListNodePoolsCall) Return(arg0 ocm.NodePoolListIterator) *MockClusterServiceClientSpecListNodePoolsCall {
	c.Call = c.Call.Return(arg0)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecListNodePoolsCall) Do(f func(ocm.InternalID, string) ocm.NodePoolListIterator) *MockClusterServiceClientSpecListNodePoolsCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecListNodePoolsCall) DoAndReturn(f func(ocm.InternalID, string) ocm.NodePoolListIterator) *MockClusterServiceClientSpecListNodePoolsCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// PostBreakGlassCredential mocks base method.
func (m *MockClusterServiceClientSpec) PostBreakGlassCredential(ctx context.Context, clusterInternalID ocm.InternalID) (*v1.BreakGlassCredential, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "PostBreakGlassCredential", ctx, clusterInternalID)
	ret0, _ := ret[0].(*v1.BreakGlassCredential)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// PostBreakGlassCredential indicates an expected call of PostBreakGlassCredential.
func (mr *MockClusterServiceClientSpecMockRecorder) PostBreakGlassCredential(ctx, clusterInternalID any) *MockClusterServiceClientSpecPostBreakGlassCredentialCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "PostBreakGlassCredential", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).PostBreakGlassCredential), ctx, clusterInternalID)
	return &MockClusterServiceClientSpecPostBreakGlassCredentialCall{Call: call}
}

// MockClusterServiceClientSpecPostBreakGlassCredentialCall wrap *gomock.Call
type MockClusterServiceClientSpecPostBreakGlassCredentialCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecPostBreakGlassCredentialCall) Return(arg0 *v1.BreakGlassCredential, arg1 error) *MockClusterServiceClientSpecPostBreakGlassCredentialCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecPostBreakGlassCredentialCall) Do(f func(context.Context, ocm.InternalID) (*v1.BreakGlassCredential, error)) *MockClusterServiceClientSpecPostBreakGlassCredentialCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecPostBreakGlassCredentialCall) DoAndReturn(f func(context.Context, ocm.InternalID) (*v1.BreakGlassCredential, error)) *MockClusterServiceClientSpecPostBreakGlassCredentialCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// PostCluster mocks base method.
func (m *MockClusterServiceClientSpec) PostCluster(ctx context.Context, cluster *v1alpha1.Cluster) (*v1alpha1.Cluster, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "PostCluster", ctx, cluster)
	ret0, _ := ret[0].(*v1alpha1.Cluster)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// PostCluster indicates an expected call of PostCluster.
func (mr *MockClusterServiceClientSpecMockRecorder) PostCluster(ctx, cluster any) *MockClusterServiceClientSpecPostClusterCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "PostCluster", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).PostCluster), ctx, cluster)
	return &MockClusterServiceClientSpecPostClusterCall{Call: call}
}

// MockClusterServiceClientSpecPostClusterCall wrap *gomock.Call
type MockClusterServiceClientSpecPostClusterCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecPostClusterCall) Return(arg0 *v1alpha1.Cluster, arg1 error) *MockClusterServiceClientSpecPostClusterCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecPostClusterCall) Do(f func(context.Context, *v1alpha1.Cluster) (*v1alpha1.Cluster, error)) *MockClusterServiceClientSpecPostClusterCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecPostClusterCall) DoAndReturn(f func(context.Context, *v1alpha1.Cluster) (*v1alpha1.Cluster, error)) *MockClusterServiceClientSpecPostClusterCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// PostNodePool mocks base method.
func (m *MockClusterServiceClientSpec) PostNodePool(ctx context.Context, clusterInternalID ocm.InternalID, nodePool *v1alpha1.NodePool) (*v1alpha1.NodePool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "PostNodePool", ctx, clusterInternalID, nodePool)
	ret0, _ := ret[0].(*v1alpha1.NodePool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// PostNodePool indicates an expected call of PostNodePool.
func (mr *MockClusterServiceClientSpecMockRecorder) PostNodePool(ctx, clusterInternalID, nodePool any) *MockClusterServiceClientSpecPostNodePoolCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "PostNodePool", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).PostNodePool), ctx, clusterInternalID, nodePool)
	return &MockClusterServiceClientSpecPostNodePoolCall{Call: call}
}

// MockClusterServiceClientSpecPostNodePoolCall wrap *gomock.Call
type MockClusterServiceClientSpecPostNodePoolCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecPostNodePoolCall) Return(arg0 *v1alpha1.NodePool, arg1 error) *MockClusterServiceClientSpecPostNodePoolCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecPostNodePoolCall) Do(f func(context.Context, ocm.InternalID, *v1alpha1.NodePool) (*v1alpha1.NodePool, error)) *MockClusterServiceClientSpecPostNodePoolCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecPostNodePoolCall) DoAndReturn(f func(context.Context, ocm.InternalID, *v1alpha1.NodePool) (*v1alpha1.NodePool, error)) *MockClusterServiceClientSpecPostNodePoolCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// UpdateCluster mocks base method.
func (m *MockClusterServiceClientSpec) UpdateCluster(ctx context.Context, internalID ocm.InternalID, cluster *v1alpha1.Cluster) (*v1alpha1.Cluster, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateCluster", ctx, internalID, cluster)
	ret0, _ := ret[0].(*v1alpha1.Cluster)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UpdateCluster indicates an expected call of UpdateCluster.
func (mr *MockClusterServiceClientSpecMockRecorder) UpdateCluster(ctx, internalID, cluster any) *MockClusterServiceClientSpecUpdateClusterCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateCluster", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).UpdateCluster), ctx, internalID, cluster)
	return &MockClusterServiceClientSpecUpdateClusterCall{Call: call}
}

// MockClusterServiceClientSpecUpdateClusterCall wrap *gomock.Call
type MockClusterServiceClientSpecUpdateClusterCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecUpdateClusterCall) Return(arg0 *v1alpha1.Cluster, arg1 error) *MockClusterServiceClientSpecUpdateClusterCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecUpdateClusterCall) Do(f func(context.Context, ocm.InternalID, *v1alpha1.Cluster) (*v1alpha1.Cluster, error)) *MockClusterServiceClientSpecUpdateClusterCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecUpdateClusterCall) DoAndReturn(f func(context.Context, ocm.InternalID, *v1alpha1.Cluster) (*v1alpha1.Cluster, error)) *MockClusterServiceClientSpecUpdateClusterCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}

// UpdateNodePool mocks base method.
func (m *MockClusterServiceClientSpec) UpdateNodePool(ctx context.Context, internalID ocm.InternalID, nodePool *v1alpha1.NodePool) (*v1alpha1.NodePool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateNodePool", ctx, internalID, nodePool)
	ret0, _ := ret[0].(*v1alpha1.NodePool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UpdateNodePool indicates an expected call of UpdateNodePool.
func (mr *MockClusterServiceClientSpecMockRecorder) UpdateNodePool(ctx, internalID, nodePool any) *MockClusterServiceClientSpecUpdateNodePoolCall {
	mr.mock.ctrl.T.Helper()
	call := mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateNodePool", reflect.TypeOf((*MockClusterServiceClientSpec)(nil).UpdateNodePool), ctx, internalID, nodePool)
	return &MockClusterServiceClientSpecUpdateNodePoolCall{Call: call}
}

// MockClusterServiceClientSpecUpdateNodePoolCall wrap *gomock.Call
type MockClusterServiceClientSpecUpdateNodePoolCall struct {
	*gomock.Call
}

// Return rewrite *gomock.Call.Return
func (c *MockClusterServiceClientSpecUpdateNodePoolCall) Return(arg0 *v1alpha1.NodePool, arg1 error) *MockClusterServiceClientSpecUpdateNodePoolCall {
	c.Call = c.Call.Return(arg0, arg1)
	return c
}

// Do rewrite *gomock.Call.Do
func (c *MockClusterServiceClientSpecUpdateNodePoolCall) Do(f func(context.Context, ocm.InternalID, *v1alpha1.NodePool) (*v1alpha1.NodePool, error)) *MockClusterServiceClientSpecUpdateNodePoolCall {
	c.Call = c.Call.Do(f)
	return c
}

// DoAndReturn rewrite *gomock.Call.DoAndReturn
func (c *MockClusterServiceClientSpecUpdateNodePoolCall) DoAndReturn(f func(context.Context, ocm.InternalID, *v1alpha1.NodePool) (*v1alpha1.NodePool, error)) *MockClusterServiceClientSpecUpdateNodePoolCall {
	c.Call = c.Call.DoAndReturn(f)
	return c
}
