package controllers

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

import (
	"encoding/json"
	"fmt"

	workv1 "open-cluster-management.io/api/work/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/Azure/ARO-HCP/internal/utils"
)

// redactedValue is the placeholder value used when redacting sensitive data.
const redactedValue = "[REDACTED]"

// RedactManifestWork returns a deep copy of the given ManifestWork with sensitive
// data redacted. It handles:
//   - Kubernetes Secrets embedded as manifests (redacts Data and StringData fields)
//   - Nested ManifestWorks that may themselves contain Secrets or further
//     ManifestWorks (applied recursively)
//   - Status feedback JsonRaw values that may contain serialized sensitive K8s
//     resources (e.g. a full Secret returned via a feedback rule)
//
// Resources whose GVK cannot be determined from their raw JSON (e.g. partial
// status objects without apiVersion/kind) are left unchanged, as their type
// cannot be reliably inferred.
//
// NOTE on the output representation of each manifest's runtime.RawExtension:
// the returned deep copy preserves only Object or only Raw, but not both: Manifests
// that entered with a typed recognized Object (*corev1.Secret, *workv1.ManifestWork) will
// have Object set and Raw nil. Manifests that entered with only Raw, or with
// an unrecognized Object type (e.g. *unstructured.Unstructured or any other CR type), will have Raw
// set and Object nil. Both states are valid for runtime.RawExtension. MarshalJSON
// handles either correctly, but callers should not assume a specific field is populated.
func RedactManifestWork(mw *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	redacted := mw.DeepCopy()
	if err := redactManifestWorkInPlace(redacted); err != nil {
		return nil, err
	}
	return redacted, nil
}

// redactManifestWorkInPlace modifies the ManifestWork in-place, redacting
// sensitive data from its spec manifests and status feedbacks. Callers must
// ensure this is called on a deep copy to avoid mutating the original.
func redactManifestWorkInPlace(mw *workv1.ManifestWork) error {
	for i := range mw.Spec.Workload.Manifests {
		if err := redactManifestInPlace(&mw.Spec.Workload.Manifests[i]); err != nil {
			return utils.TrackError(fmt.Errorf("redacting spec manifest at index %d: %w", i, err))
		}
	}

	for i := range mw.Status.ResourceStatus.Manifests {
		if err := redactManifestConditionFeedbacksInPlace(&mw.Status.ResourceStatus.Manifests[i]); err != nil {
			return utils.TrackError(fmt.Errorf("redacting status feedback at manifest condition index %d: %w", i, err))
		}
	}

	return nil
}

// redactManifestInPlace inspects the embedded resource within a workv1.Manifest
// and redacts sensitive data in-place.
//
// A runtime.RawExtension (embedded in workv1.Manifest) can have three states:
//  1. Object is set, Raw is nil
//  2. Raw is set, Object is nil
//  3. Both Object and Raw are set

// This function prioritizes Object when it is set, using Go type assertions for
// maximum type safety. In every branch of the Object-is-set path, Raw is either
// nilled out or overwritten to ensure that any pre-existing Raw bytes (which may
// contain unredacted sensitive data) are never left intact. When Object is nil,
// we fall through to the Raw-only path.

// ASSUMPTION: when both Object and Raw are set, they represent the same resource.
// If this invariant were ever violated (e.g. Object is a ConfigMap but Raw contains a
// Secret), we would redact based on Object's type and discard Raw. Since Raw is
// always nilled out or overwritten in the Object-is-set path, sensitive data
// that may have been present in Raw can never be logged. The only consequence
// of the invariant being violated would be an incomplete redacted representation
// of the manifest (missing whatever Raw contained), not a sensitive data leak.
func redactManifestInPlace(m *workv1.Manifest) error {
	if m.Object != nil {
		switch obj := m.Object.(type) {
		case *corev1.Secret:
			redactSecretFieldsInPlace(obj)
			// Nil out Raw to prevent leaking unredacted sensitive data that
			// may be present in pre-existing raw bytes. After this, callers
			// serializing the manifest will use the redacted Object.
			m.Raw = nil
			return nil
		case *workv1.ManifestWork:
			if err := redactManifestWorkInPlace(obj); err != nil {
				return utils.TrackError(fmt.Errorf("redacting nested typed ManifestWork: %w", err))
			}
			// Nil out Raw for the same reason as the Secret case above.
			m.Raw = nil
			return nil
		default:
			// Any other typed K8s object (e.g. *unstructured.Unstructured or a
			// different CR type. Serialize to raw JSON overwriting any pre-existing Raw
			// bytes and process via the raw path so we can still detect
			// Secrets/ManifestWorks by GVK.
			raw, err := json.Marshal(obj)
			if err != nil {
				return utils.TrackError(fmt.Errorf("marshaling typed object to JSON for redaction: %w", err))
			}
			m.Raw = raw
			// Nil out Object to prevent leaking unredacted sensitive data that
			// may be present in pre-existing Object. After this, callers
			// serializing the manifest will use the redacted Raw.
			m.Object = nil
			return redactManifestFromRawInPlace(m)
		}
	}

	// Object is nil - process the raw JSON bytes directly.
	if len(m.Raw) > 0 {
		return redactManifestFromRawInPlace(m)
	}

	return nil
}

// redactManifestFromRawInPlace detects the GVK of the embedded resource from
// its raw JSON representation and applies type-specific redaction in-place for
// Secrets and ManifestWorks.
// The manifest's Object field is always nilled out when redaction is successful.
// If the GVK cannot be determined, nothing is redacted.
func redactManifestFromRawInPlace(m *workv1.Manifest) error {
	gvk, ok := detectGVKFromRaw(m.Raw)
	if !ok {
		return nil // can't determine type; nothing to redact
	}

	switch {
	case isSecretGK(gvk):
		redactedRaw, err := redactSecretFromRawJSON(m.Raw)
		if err != nil {
			return utils.TrackError(fmt.Errorf("redacting raw Secret manifest: %w", err))
		}
		m.Raw = redactedRaw
		m.Object = nil
		return nil
	case isManifestWorkGK(gvk):
		redactedRaw, err := redactManifestWorkFromRawJSON(m.Raw)
		if err != nil {
			return utils.TrackError(fmt.Errorf("redacting raw nested ManifestWork manifest: %w", err))
		}
		m.Raw = redactedRaw
		m.Object = nil
		return nil
	default:
		return nil
	}
}

// --- Secret redaction ---

// redactSecretFieldsInPlace zeroes out the Data and StringData fields on a
// typed corev1.Secret in-place, replacing each value with the redacted placeholder.
func redactSecretFieldsInPlace(secret *corev1.Secret) {
	for k := range secret.Data {
		secret.Data[k] = []byte(redactedValue)
	}
	for k := range secret.StringData {
		secret.StringData[k] = redactedValue
	}
}

// redactSecretFromRawJSON deserializes raw JSON into a corev1.Secret, redacts its
// sensitive fields, and returns the re-serialized JSON.
func redactSecretFromRawJSON(raw []byte) ([]byte, error) {
	var secret corev1.Secret
	if err := json.Unmarshal(raw, &secret); err != nil {
		return nil, utils.TrackError(fmt.Errorf("unmarshaling Secret: %w", err))
	}

	redactSecretFieldsInPlace(&secret)

	redacted, err := json.Marshal(&secret)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("marshaling redacted Secret: %w", err))
	}
	return redacted, nil
}

// --- Nested ManifestWork redaction ---

// redactManifestWorkFromRawJSON deserializes raw JSON into a workv1.ManifestWork,
// recursively redacts sensitive data, and returns the re-serialized JSON.
func redactManifestWorkFromRawJSON(raw []byte) ([]byte, error) {
	var mw workv1.ManifestWork
	if err := json.Unmarshal(raw, &mw); err != nil {
		return nil, utils.TrackError(fmt.Errorf("unmarshaling nested ManifestWork: %w", err))
	}

	if err := redactManifestWorkInPlace(&mw); err != nil {
		return nil, utils.TrackError(fmt.Errorf("redacting nested ManifestWork: %w", err))
	}

	redacted, err := json.Marshal(&mw)
	if err != nil {
		return nil, utils.TrackError(fmt.Errorf("marshaling redacted ManifestWork: %w", err))
	}
	return redacted, nil
}

// --- Status feedback redaction ---

// redactManifestConditionFeedbacksInPlace redacts JsonRaw values within status
// feedback entries in-place that may contain serialized sensitive K8s resources
// (e.g. a full Secret returned via the "@" json path feedback rule).
func redactManifestConditionFeedbacksInPlace(mc *workv1.ManifestCondition) error {
	for i := range mc.StatusFeedbacks.Values {
		if err := redactFeedbackValueInPlace(&mc.StatusFeedbacks.Values[i]); err != nil {
			return utils.TrackError(fmt.Errorf("redacting feedback value %q: %w", mc.StatusFeedbacks.Values[i].Name, err))
		}
	}
	return nil
}

// redactFeedbackValueInPlace checks a single FeedbackValue's JsonRaw field for
// embedded sensitive K8s resources and redacts them in-place if found. Values
// without JsonRaw (integer, string, boolean types) are left unchanged.
func redactFeedbackValueInPlace(fv *workv1.FeedbackValue) error {
	if fv.Value.JsonRaw == nil {
		return nil
	}

	raw := []byte(*fv.Value.JsonRaw)

	gvk, ok := detectGVKFromRaw(raw)
	if !ok {
		// Not a recognizable K8s resource (e.g. a partial status field
		// without apiVersion/kind). Cannot determine sensitivity.
		return nil
	}

	var (
		redactedRaw []byte
		err         error
	)
	switch {
	case isSecretGK(gvk):
		redactedRaw, err = redactSecretFromRawJSON(raw)
	case isManifestWorkGK(gvk):
		redactedRaw, err = redactManifestWorkFromRawJSON(raw)
	default:
		return nil
	}
	if err != nil {
		return err
	}

	redacted := string(redactedRaw)
	fv.Value.JsonRaw = &redacted
	return nil
}

// --- GVK detection helpers ---

// detectGVKFromRaw extracts the GroupVersionKind from raw JSON by reading only the
// apiVersion and kind fields. Returns false if the JSON cannot be parsed or
// does not contain a kind field.
func detectGVKFromRaw(raw []byte) (schema.GroupVersionKind, bool) {
	var typeMeta metav1.TypeMeta
	err := json.Unmarshal(raw, &typeMeta)
	if err != nil {
		return schema.GroupVersionKind{}, false
	}
	if typeMeta.Kind == "" {
		return schema.GroupVersionKind{}, false
	}
	return typeMeta.GroupVersionKind(), true
}

// isSecretGK returns true if the GVK represents a core Secret resource.
// The version is intentionally not checked so that any API version is matched.
func isSecretGK(gvk schema.GroupVersionKind) bool {
	return gvk.Group == "" && gvk.Kind == "Secret"
}

// isManifestWorkGK returns true if the GVK represents an open-cluster-management
// ManifestWork resource. The version is intentionally not checked so that any
// API version is matched.
func isManifestWorkGK(gvk schema.GroupVersionKind) bool {
	return gvk.Group == "work.open-cluster-management.io" && gvk.Kind == "ManifestWork"
}
