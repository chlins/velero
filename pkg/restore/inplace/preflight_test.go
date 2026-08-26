/*
Copyright the Velero contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inplace

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1api "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	velerotest "github.com/vmware-tanzu/velero/pkg/test"
)

func podUsingPVC(name, pvcName string, phase corev1api.PodPhase, terminating bool) *corev1api.Pod {
	pod := &corev1api.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name + "-uid"),
		},
		Spec: corev1api.PodSpec{
			Volumes: []corev1api.Volume{
				{
					Name: "data",
					VolumeSource: corev1api.VolumeSource{
						PersistentVolumeClaim: &corev1api.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
		Status: corev1api.PodStatus{Phase: phase},
	}
	if terminating {
		now := metav1.Now()
		pod.DeletionTimestamp = &now
		pod.Finalizers = []string{"fake-finalizer"}
	}
	return pod
}

func TestCheckPVCNotInUse(t *testing.T) {
	tests := []struct {
		name          string
		pods          []*corev1api.Pod
		excludedPods  []string
		expectPass    bool
		expectMessage []string
	}{
		{
			name:       "no pods, check passes",
			expectPass: true,
		},
		{
			name:          "active pod blocks with the delete hint",
			pods:          []*corev1api.Pod{podUsingPVC("pod-1", "pvc-1", corev1api.PodRunning, false)},
			expectMessage: []string{"pod-1 (Running)", "delete the workloads"},
		},
		{
			name:          "unknown-phase pod blocks (node may be unreachable)",
			pods:          []*corev1api.Pod{podUsingPVC("pod-1", "pvc-1", corev1api.PodUnknown, false)},
			expectMessage: []string{"pod-1 (Unknown)"},
		},
		{
			name:          "terminating pod blocks with the wait hint",
			pods:          []*corev1api.Pod{podUsingPVC("pod-1", "pvc-1", corev1api.PodRunning, true)},
			expectMessage: []string{"pod-1 (Running, terminating)", "retry after they are fully removed"},
		},
		{
			name: "terminal-phase pods do not block",
			pods: []*corev1api.Pod{
				podUsingPVC("pod-1", "pvc-1", corev1api.PodSucceeded, false),
				podUsingPVC("pod-2", "pvc-1", corev1api.PodFailed, false),
			},
			expectPass: true,
		},
		{
			name:       "pod using another PVC does not block",
			pods:       []*corev1api.Pod{podUsingPVC("pod-1", "other-pvc", corev1api.PodRunning, false)},
			expectPass: true,
		},
		{
			name: "excluded pod does not block, other pods still do",
			pods: []*corev1api.Pod{
				podUsingPVC("restored-pod", "pvc-1", corev1api.PodRunning, false),
				podUsingPVC("other-pod", "pvc-1", corev1api.PodRunning, false),
			},
			excludedPods:  []string{"restored-pod"},
			expectMessage: []string{"[other-pod (Running)]"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			objs := []runtime.Object{}
			for _, pod := range tc.pods {
				objs = append(objs, pod)
			}
			cli := velerotest.NewFakeControllerRuntimeClient(t, objs...)

			pvc := &corev1api.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc-1", Namespace: "default"},
			}

			err := CheckPVCNotInUse(t.Context(), cli, pvc, tc.excludedPods...)
			if tc.expectPass {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "pre-flight check failed")
			for _, fragment := range tc.expectMessage {
				assert.Contains(t, err.Error(), fragment)
			}
		})
	}
}
