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

package aks

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCheckClusterAdminPermissions(t *testing.T) {
	requestError := apierrors.NewUnauthorized("expired credentials")
	for _, tc := range []struct {
		name    string
		status  authorizationv1.SubjectAccessReviewStatus
		err     error
		wantErr string
		denied  bool
	}{
		{name: "allowed", status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}},
		{name: "denied", status: authorizationv1.SubjectAccessReviewStatus{Denied: true, Reason: "read-only access"}, wantErr: "read-only access", denied: true},
		{name: "no opinion", wantErr: "cluster admin permissions denied", denied: true},
		{name: "request failure", err: requestError, wantErr: "expired credentials"},
		{name: "evaluation failure", status: authorizationv1.SubjectAccessReviewStatus{EvaluationError: "authorizer unavailable"}, wantErr: "authorizer unavailable"},
		{name: "allowed with evaluation failure", status: authorizationv1.SubjectAccessReviewStatus{Allowed: true, EvaluationError: "authorizer unavailable"}, wantErr: "authorizer unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset()
			client.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
				createAction, ok := action.(k8stesting.CreateAction)
				require.True(t, ok)
				review, ok := createAction.GetObject().(*authorizationv1.SelfSubjectAccessReview)
				require.True(t, ok)
				require.Equal(t, &authorizationv1.ResourceAttributes{
					Group: "*", Resource: "*", Verb: "*",
				}, review.Spec.ResourceAttributes)
				require.Nil(t, review.Spec.NonResourceAttributes)
				return true, &authorizationv1.SelfSubjectAccessReview{Status: tc.status}, tc.err
			})

			err := checkClusterAdminPermissions(context.Background(), client)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
			require.Equal(t, tc.denied, errors.Is(err, errClusterAdminPermissionsDenied))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			}
			require.Len(t, client.Actions(), 1, "the check must only issue an authorization review")
		})
	}
}

func TestEnsureClusterAdminReadOnlyAccess(t *testing.T) {
	client := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "readable", Namespace: "default"},
	})
	_, err := client.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err, "pod listing must succeed to reproduce the old false-positive check")
	client.ClearActions()

	assigned := false
	reviews := 0
	client.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		reviews++
		return true, &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{Allowed: assigned},
		}, nil
	})
	err = ensureClusterAdmin(context.Background(), nil, func(ctx context.Context) error {
		return checkClusterAdminPermissions(ctx, client)
	}, func(context.Context) error {
		assigned = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, assigned, "read-only access must not skip the admin assignment")
	require.Equal(t, 2, reviews, "authorization must be checked before and after assignment")
}

func TestEnsureClusterAdmin(t *testing.T) {
	requestError := apierrors.NewForbidden(schema.GroupResource{Group: "authorization.k8s.io", Resource: "selfsubjectaccessreviews"}, "", errors.New("review forbidden"))
	assignmentError := errors.New("assignment failed")
	for _, tc := range []struct {
		name            string
		checkErrors     []error
		assignmentError error
		wantErr         error
		wantAssignments int
	}{
		{name: "already allowed", checkErrors: []error{nil}},
		{name: "denied then allowed", checkErrors: []error{errClusterAdminPermissionsDenied, nil}, wantAssignments: 1},
		{name: "polls until allowed", checkErrors: []error{errClusterAdminPermissionsDenied, errClusterAdminPermissionsDenied, errClusterAdminPermissionsDenied, nil}, wantAssignments: 1},
		{name: "initial request failure", checkErrors: []error{requestError}, wantErr: requestError},
		{name: "initial cancellation", checkErrors: []error{context.Canceled}, wantErr: context.Canceled},
		{name: "assignment failure", checkErrors: []error{errClusterAdminPermissionsDenied}, assignmentError: assignmentError, wantErr: assignmentError, wantAssignments: 1},
		{name: "request failure after assignment", checkErrors: []error{errClusterAdminPermissionsDenied, requestError}, wantErr: requestError, wantAssignments: 1},
		{name: "request failure during polling", checkErrors: []error{errClusterAdminPermissionsDenied, errClusterAdminPermissionsDenied, requestError}, wantErr: requestError, wantAssignments: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks, assignments := 0, 0
			err := ensureClusterAdmin(context.Background(), &ClusterAdminAssignmentOptions{
				Timeout: time.Second, CheckFrequency: time.Millisecond,
			}, func(context.Context) error {
				require.Less(t, checks, len(tc.checkErrors), "unexpected extra authorization check")
				err := tc.checkErrors[checks]
				checks++
				return err
			}, func(context.Context) error {
				assignments++
				return tc.assignmentError
			})
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
			require.Equal(t, len(tc.checkErrors), checks)
			require.Equal(t, tc.wantAssignments, assignments)
		})
	}
}

func TestEnsureClusterAdminTimeout(t *testing.T) {
	assignments := 0
	err := ensureClusterAdmin(context.Background(), &ClusterAdminAssignmentOptions{
		Timeout: 10 * time.Millisecond, CheckFrequency: time.Millisecond,
	}, func(context.Context) error {
		return errClusterAdminPermissionsDenied
	}, func(context.Context) error {
		assignments++
		return nil
	})
	require.ErrorContains(t, err, "timed out waiting for role assignment to be effective")
	require.ErrorIs(t, err, errClusterAdminPermissionsDenied)
	require.Equal(t, 1, assignments)
}

func TestEnsureClusterAdminCancellationDuringPolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checks := 0
	err := ensureClusterAdmin(ctx, &ClusterAdminAssignmentOptions{
		Timeout: time.Second, CheckFrequency: time.Millisecond,
	}, func(context.Context) error {
		checks++
		if checks == 3 {
			cancel()
		}
		return errClusterAdminPermissionsDenied
	}, func(context.Context) error {
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.GreaterOrEqual(t, checks, 3)
}

func TestCheckClusterAdminPermissionsInvalidKubeconfig(t *testing.T) {
	err := CheckClusterAdminPermissions(context.Background(), filepath.Join(t.TempDir(), "missing"))
	require.ErrorContains(t, err, "failed to create Kubernetes client")
	require.NotErrorIs(t, err, errClusterAdminPermissionsDenied)
}
