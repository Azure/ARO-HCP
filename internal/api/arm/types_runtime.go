package arm

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	_ runtime.Object            = &Subscription{}
	_ metav1.ObjectMetaAccessor = &Subscription{}
)

func (s *Subscription) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

// GetObjectMeta returns metadata that allows Kubernetes informer machinery
// to key subscriptions by their subscription ID.
func (s *Subscription) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if s.ResourceID != nil {
		om.Name = strings.ToLower(s.ResourceID.String())
	}
	return om
}
