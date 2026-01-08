package controller

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

func deletionHandlingControllerMetaNamespaceKeyFunc(obj interface{}) (string, error) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return "", fmt.Errorf("error decoding object, invalid type")
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return "", fmt.Errorf("error decoding object tombstone, invalid type")
		}
	}

	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		if ownerRef.Kind != "Session" {
			return "", fmt.Errorf("object is not owned by a Session")
		}

		namespace := object.GetNamespace()
		if namespace == "" {
			return ownerRef.Name, nil
		}
		return fmt.Sprintf("%s/%s", namespace, ownerRef.Name), nil
	}
	return "", fmt.Errorf("object has no controller owner reference")
}

func EnqueueOwningSession(obj runtime.Object) []string {
	key, err := deletionHandlingControllerMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.V(4).ErrorS(err, "could not determine owning session queue key")
		return nil
	}
	klog.V(4).InfoS("enqueueing owning session", "key", key)
	return []string{key}
}
