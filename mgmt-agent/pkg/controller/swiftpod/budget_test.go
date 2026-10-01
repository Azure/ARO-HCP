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

package swiftpod

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

func TestAccountingValidation(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "corrupt", "version", "zero-window", "negative-window", "future",
		"zero-time", "missing-workload", "missing-node", "missing-pod", "empty-id", "owner", "deleting", "oversized", "increase"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			cm, err := f.kube.CoreV1().ConfigMaps("agent").Get(context.Background(), BudgetName, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			record := evictionRecord{WorkloadUID: "deployment", NodeUID: f.node.UID, PodUID: f.pod.UID, AttemptedAt: metav1.NewTime(f.now)}
			l := ledger{Version: 1, EvictionWindow: f.cfg.EvictionWindow, Evictions: map[string]evictionRecord{}}
			switch kind {
			case "version":
				l.Version = 2
			case "zero-window":
				l.EvictionWindow.Duration = 0
			case "negative-window":
				l.EvictionWindow.Duration = -time.Second
			case "future":
				record.AttemptedAt = metav1.NewTime(f.now.Add(time.Second))
			case "zero-time":
				record.AttemptedAt = metav1.Time{}
			case "missing-workload":
				record.WorkloadUID = ""
			case "missing-node":
				record.NodeUID = ""
			case "missing-pod":
				record.PodUID = ""
			case "owner":
				cm.OwnerReferences = []metav1.OwnerReference{{Kind: "Node", Name: "a", UID: f.node.UID}}
			case "deleting":
				cm.DeletionTimestamp = &metav1.Time{Time: f.now}
			case "increase":
				l.EvictionWindow.Duration = time.Minute
			}
			if kind != "increase" {
				l.Evictions["attempt"] = record
			}
			if kind == "empty-id" {
				l.Evictions[""] = record
			}
			if kind == "oversized" {
				for i := range maxRecords + 1 {
					l.Evictions[fmt.Sprint(i)] = record
				}
			}
			data, err := json.Marshal(l)
			if err != nil {
				t.Fatal(err)
			}
			cm.Data[budgetKey] = string(data)
			if kind == "empty" {
				delete(cm.Data, budgetKey)
			}
			if kind == "corrupt" {
				cm.Data[budgetKey] = "{"
			}
			if kind == "missing" {
				if err := f.kube.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("configmaps"), "agent", BudgetName); err != nil {
					t.Fatal(err)
				}
			} else if err := f.kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("configmaps"), cm, "agent"); err != nil {
				t.Fatal(err)
			}
			f.kube.ClearActions()
			if err := f.run(); err == nil {
				t.Fatal("invalid accounting admitted")
			}
			if len(mutations(f.kube.Actions())) != 0 {
				t.Fatal("invalid accounting made writes")
			}
		})
	}
}

func TestAccountingWindowAndLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		age    time.Duration
		window time.Duration
		want   bool
	}{
		{"decrease retained", time.Minute, 30 * time.Minute, false},
		{"decrease expired", time.Hour + time.Second, 30 * time.Minute, true},
		{"increase expired", 3 * time.Hour, 2 * time.Hour, false},
		{"retained boundary", time.Hour, time.Hour, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			cfg := f.cfg
			cfg.EvictionWindow.Duration = test.window
			f.kube.PrependReactor("get", "configmaps", func(action ktesting.Action) (bool, runtime.Object, error) {
				a := action.(ktesting.GetAction)
				obj, err := f.kube.Tracker().Get(a.GetResource(), a.GetNamespace(), a.GetName())
				if err != nil {
					return true, nil, err
				}
				cm := obj.(*corev1.ConfigMap)
				l := ledger{Version: 1, EvictionWindow: f.cfg.EvictionWindow, Evictions: map[string]evictionRecord{
					"attempt": {WorkloadUID: "deployment", NodeUID: f.node.UID, PodUID: f.pod.UID, AttemptedAt: metav1.NewTime(f.now.Add(-test.age))}}}
				data, err := json.Marshal(l)
				if err != nil {
					return true, nil, err
				}
				cm.Data[budgetKey] = string(data)
				return true, cm, nil
			})
			b, err := f.c.readBudget(context.Background(), cfg)
			if (err == nil) != test.want {
				t.Fatalf("error=%v", err)
			}
			if test.name == "retained boundary" && len(b.Evictions) != 1 {
				t.Fatal("boundary history was pruned")
			}
			if len(mutations(f.kube.Actions())) != 0 {
				t.Fatal("read/prune wrote accounting")
			}
		})
	}
	f := newFixture(t)
	b := &budget{ledger: ledger{Evictions: map[string]evictionRecord{}}}
	for i := range f.cfg.MaxEvictionsPerWorkload {
		b.Evictions[fmt.Sprint(i)] = evictionRecord{WorkloadUID: "deployment", NodeUID: "other", AttemptedAt: metav1.NewTime(f.now.Add(-2 * time.Minute))}
	}
	if evictionAllowance(b, f.cfg, "deployment", f.node.UID, f.now) == nil {
		t.Fatal("workload limit ignored")
	}
	if err := evictionAllowance(b, f.cfg, "independent", f.node.UID, f.now); err != nil {
		t.Fatal(err)
	}
	for i := range f.cfg.MaxEvictionsPerNode {
		b.Evictions[fmt.Sprint(i)] = evictionRecord{WorkloadUID: "other", NodeUID: f.node.UID, AttemptedAt: metav1.NewTime(f.now.Add(-2 * time.Minute))}
	}
	if evictionAllowance(b, f.cfg, "independent", f.node.UID, f.now) == nil {
		t.Fatal("node limit ignored")
	}
}
