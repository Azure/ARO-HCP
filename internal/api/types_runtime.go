package api

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	_ runtime.Object            = &HCPOpenShiftCluster{}
	_ metav1.ObjectMetaAccessor = &HCPOpenShiftCluster{}
)

func (o *HCPOpenShiftCluster) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *HCPOpenShiftCluster) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ID != nil {
		om.Name = strings.ToLower(o.ID.String())
	}
	return om
}

var (
	_ runtime.Object            = &HCPOpenShiftClusterNodePool{}
	_ metav1.ObjectMetaAccessor = &HCPOpenShiftClusterNodePool{}
)

func (o *HCPOpenShiftClusterNodePool) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *HCPOpenShiftClusterNodePool) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ID != nil {
		om.Name = strings.ToLower(o.ID.String())
	}
	return om
}

var (
	_ runtime.Object            = &Operation{}
	_ metav1.ObjectMetaAccessor = &Operation{}
)

func (o *Operation) GetObjectKind() schema.ObjectKind {
	return schema.EmptyObjectKind
}

func (o *Operation) GetObjectMeta() metav1.Object {
	om := &metav1.ObjectMeta{}
	if o.ResourceID != nil {
		om.Name = strings.ToLower(o.ResourceID.String())
	}
	return om
}
