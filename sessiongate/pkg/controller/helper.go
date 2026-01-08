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
			return "", fmt.Errorf("object is not a Session")
		}

		return cache.MetaNamespaceKeyFunc(&metav1.ObjectMeta{
			Name:      ownerRef.Name,
			Namespace: object.GetNamespace(),
		})
	}
	return "", fmt.Errorf("object has no owner reference")
}

func EnqueueOwningSession(obj runtime.Object) []string {
	key, err := deletionHandlingControllerMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	return []string{key}
}
