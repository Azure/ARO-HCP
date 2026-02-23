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

package controller

import (
	"fmt"
	"hash/fnv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	sessiongatev1alpha1 "github.com/Azure/ARO-HCP/sessiongate/pkg/apis/sessiongate/v1alpha1"
)

func registerInformer[T comparable](informer cache.SharedIndexInformer, keyFunc func(obj interface{}) (T, error), workQueue workqueue.TypedRateLimitingInterface[T]) error {
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := keyFunc(obj)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
		UpdateFunc: func(old, new interface{}) {
			key, err := keyFunc(new)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
		DeleteFunc: func(obj interface{}) {
			key, err := keyFunc(obj)
			if err != nil {
				return
			}
			workQueue.Add(key)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler for informer: %w", err)
	}
	return nil
}

// keyForObject extracts the object namespace/name workqueue key from objects.
func keyForObject(obj interface{}) (cache.ObjectName, error) {
	key, err := cache.DeletionHandlingObjectToName(obj)
	if err != nil {
		return cache.ObjectName{}, fmt.Errorf("could not determine queue key: %w", err)
	}
	return key, nil
}

func mgmtClusterResourceIdFromSession(obj interface{}) (string, error) {
	// obj needs to be a session
	session, ok := obj.(*sessiongatev1alpha1.Session)
	if !ok {
		return "", fmt.Errorf("error decoding object, invalid type")
	}
	return session.Spec.ManagementCluster.ResourceID, nil
}

// sessionKeyFromOwnershipReference extracts the Session namespace/name workqueue
// key from on-cluster resources owned by a Session via a controller owner reference.
func sessionKeyFromOwnershipReference(obj interface{}) (cache.ObjectName, error) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return cache.ObjectName{}, fmt.Errorf("error decoding object, invalid type")
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			return cache.ObjectName{}, fmt.Errorf("error decoding object tombstone, invalid type")
		}
	}
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		if ownerRef.Kind != "Session" {
			return cache.ObjectName{}, fmt.Errorf("object is not owned by a Session")
		}
		return cache.NewObjectName(object.GetNamespace(), ownerRef.Name), nil
	}
	return cache.ObjectName{}, fmt.Errorf("object has no controller owner reference")
}

// sessionKeyFromOwnershipAnnotation extracts the Session namespace/name workqueue
// key from resources on a management cluster that are associated with a Session via
// an ownership annotation. Owner references aren't possible for cross-cluster resources
// like CSRs and CSRApprovals.
func sessionKeyFromOwnershipAnnotation(obj interface{}) (cache.ObjectName, error) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return cache.ObjectName{}, fmt.Errorf("error decoding object, invalid type")
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			return cache.ObjectName{}, fmt.Errorf("error decoding object tombstone, invalid type")
		}
	}
	if sessiongateAnnotation, ok := object.GetAnnotations()[AnnotationSessiongate]; ok {
		namespace, name, err := cache.SplitMetaNamespaceKey(sessiongateAnnotation)
		if err != nil {
			return cache.ObjectName{}, fmt.Errorf("failed to split meta namespace key: %w", err)
		}
		return cache.NewObjectName(namespace, name), nil
	}
	return cache.ObjectName{}, fmt.Errorf("object has no sessiongate ownership annotation")
}

func getDeterministicSuffixForSession(namespace, name string) string {
	hasher := fnv.New32a()
	fmt.Fprintf(hasher, "%s-%s", namespace, name)
	return fmt.Sprintf("%x", hasher.Sum32())
}

// sessionKeysForHCP looks up session keys associated with a HostedControlPlane via
// the sessionsByHostedControlPlaneIndexName index. Since we don't have an HCP resource
// ID on the HostedControlPlane object (yet), we use the management cluster resource ID
// and the HCP namespace as the composite index key.
//
// Migration assumption: Once the HCP resource ID is available on the HostedControlPlane
// object, this index should switch to using it directly instead of the composite key.
// This would simplify the lookup and decouple it from the management cluster resource ID.
func (c *SessionController) sessionKeysForHCP(mgmtClusterResourceID string, hcp *hypershiftv1beta1.HostedControlPlane) []cache.ObjectName {
	objs, err := c.sessiongateInformers.Sessiongate().V1alpha1().Sessions().Informer().GetIndexer().ByIndex(
		sessionsByHostedControlPlaneIndexName,
		hostedControlPlaneIndexKey(mgmtClusterResourceID, hcp.Namespace),
	)
	if err != nil {
		klog.ErrorS(err, "failed to get sessions by hosted control plane", "namespace", hcp.Namespace)
		return nil
	}
	sessionKeys := make([]cache.ObjectName, 0, len(objs))
	for _, obj := range objs {
		session, ok := obj.(*sessiongatev1alpha1.Session)
		if !ok {
			continue
		}
		sessionKeys = append(sessionKeys, cache.NewObjectName(session.Namespace, session.Name))
	}
	return sessionKeys
}
