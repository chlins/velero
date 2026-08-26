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

// Package inplace holds the pre-flight checks for in-place volume data
// restores. The checks must pass before Velero performs any side effect on
// the existing PVC/PV.
package inplace

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"
	corev1api "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vmware-tanzu/velero/pkg/util"
)

// CheckPVCNotInUse verifies the target PVC is not used by any active pod,
// aligned with the pvc-protection controller semantics: terminal-phase pods
// (Succeeded/Failed) don't block; all other phases do, and terminating pods
// are flagged so the message can hint the user to wait. excludedPods are
// exempted, e.g. the restored target pod itself on the file system path.
func CheckPVCNotInUse(
	ctx context.Context,
	cli crclient.Client,
	pvc *corev1api.PersistentVolumeClaim,
	excludedPods ...string,
) error {
	podList := new(corev1api.PodList)
	if err := cli.List(ctx, podList, &crclient.ListOptions{Namespace: pvc.Namespace}); err != nil {
		return errors.Wrapf(err, "failed to check whether PVC %s/%s is in use: failed to list pods in namespace %s", pvc.Namespace, pvc.Name, pvc.Namespace)
	}

	users := []string{}
	terminatingOnly := true
	for i := range podList.Items {
		pod := &podList.Items[i]
		if !podUsesPVC(pod, pvc.Name) ||
			pod.Status.Phase == corev1api.PodSucceeded || pod.Status.Phase == corev1api.PodFailed ||
			util.Contains(excludedPods, pod.Name) {
			continue
		}
		state := string(pod.Status.Phase)
		if pod.DeletionTimestamp != nil {
			state += ", terminating"
		} else {
			terminatingOnly = false
		}
		users = append(users, fmt.Sprintf("%s (%s)", pod.Name, state))
	}
	if len(users) == 0 {
		return nil
	}

	hint := "delete the workloads consuming the PVC and retry"
	if terminatingOnly {
		hint = "the pod(s) are terminating; retry after they are fully removed"
	}
	return errors.Errorf("in-place restore pre-flight check failed, skipping volume data restore: PVC %s/%s is still in use by pod(s) [%s]: %s",
		pvc.Namespace, pvc.Name, strings.Join(users, ", "), hint)
}

func podUsesPVC(pod *corev1api.Pod, pvcName string) bool {
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == pvcName {
			return true
		}
	}
	return false
}
