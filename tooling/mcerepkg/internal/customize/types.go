package customize

import "k8s.io/apimachinery/pkg/runtime/schema"

var deployGVK = schema.GroupVersionKind{
	Group:   "apps",
	Version: "v1",
	Kind:    "Deployment",
}
