/*
Copyright 2015 The Kubernetes Authors.

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

package pleg

import (
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	internalapi "k8s.io/cri-api/pkg/apis"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
	"k8s.io/klog/v2"
	kubecontainer "k8s.io/kubernetes/pkg/kubelet/container"
	"k8s.io/utils/clock"
)

type EventedPLEG struct {
	// The period for relisting.
	relistPeriod time.Duration
	// The container runtime.
	runtime kubecontainer.Runtime

	runtimeService internalapi.RuntimeService

	genericPLEG PodLifecycleEventGenerator

	// The channel from which the subscriber listens events.
	eventChannel chan *PodLifecycleEvent

	// Time of the last relisting.
	relistTime atomic.Value
	// Cache for storing the runtime states required for syncing pods.
	cache kubecontainer.Cache
	// For testability.
	clock clock.Clock
	// Pods that failed to have their status retrieved during a relist. These pods will be
	// retried during the next relisting.
	podsToReinspect map[types.UID]*kubecontainer.Pod
}

// NewGenericPLEG instantiates a new GenericPLEG object and return it.
func NewEventedPLEG(runtime kubecontainer.Runtime, runtimeService internalapi.RuntimeService, genericPLEG PodLifecycleEventGenerator, eventChannel chan *PodLifecycleEvent,
	relistPeriod time.Duration, cache kubecontainer.Cache, clock clock.Clock) PodLifecycleEventGenerator {
	return &EventedPLEG{
		relistPeriod:   relistPeriod,
		runtime:        runtime,
		runtimeService: runtimeService,
		genericPLEG:    genericPLEG,
		eventChannel:   eventChannel,
		cache:          cache,
		clock:          clock,
	}
}

// Watch returns a channel from which the subscriber can receive PodLifecycleEvent
// events.
// TODO: support multiple subscribers.
func (e *EventedPLEG) Watch() chan *PodLifecycleEvent {
	return e.eventChannel
}

// Start spawns a goroutine to relist periodically.
func (e *EventedPLEG) Start() {
	go wait.Forever(e.watchEventsChannel, 0)
}

// Healthy check if PLEG work properly.
// relistThreshold is the maximum interval between two relist.
func (e *EventedPLEG) Healthy() (bool, error) {
	return true, nil
}

func (e *EventedPLEG) Relist() {
	e.genericPLEG.Relist()
}

func (e *EventedPLEG) watchEventsChannel() {

	containerEventsResponseCh := make(chan *runtimeapi.ContainerEventResponse, 100000)
	defer close(containerEventsResponseCh)
	go e.runtimeService.GetContainerEvents(containerEventsResponseCh)

	for event := range containerEventsResponseCh {
		switch event.ContainerEventType {
		case runtimeapi.ContainerEventType_CONTAINER_STOPPED_EVENT:
			// e.Relist()
			time.Sleep(2 * time.Second) // TODO - See why without this delay the pod states are not updated properly

			e.updatePodStatus(event)

			e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerDied, Data: event.ContainerId}
			// return err
			klog.V(2).InfoS("Recieved Container Stopped Event", "CRI container event", event)
		case runtimeapi.ContainerEventType_CONTAINER_CREATED_EVENT:
			// e.Relist()
			time.Sleep(2 * time.Second)

			e.updatePodStatus(event)

			e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerChanged, Data: event.ContainerId}
			klog.V(2).InfoS("Recieved Container Created Event", "CRI container event", event)
		case runtimeapi.ContainerEventType_CONTAINER_STARTED_EVENT:
			// e.Relist()
			time.Sleep(2 * time.Second)

			e.updatePodStatus(event)

			e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerStarted, Data: event.ContainerId}
			klog.V(2).InfoS("Recieved Container Started Event", "CRI container event", event)
		case runtimeapi.ContainerEventType_CONTAINER_DELETED_EVENT:
			// e.Relist()
			time.Sleep(2 * time.Second)

			e.updatePodStatus(event)
			e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerDied, Data: event.ContainerId}
			e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerRemoved, Data: event.ContainerId}

			klog.V(2).InfoS("Recieved Container Deleted Event", "CRI container event", event)
		}
	}
}

func (e *EventedPLEG) updatePodStatus(event *runtimeapi.ContainerEventResponse) *PodLifecycleEvent {

	podID := types.UID(event.PodSandboxMetadata.Uid)
	podName := event.PodSandboxMetadata.Name
	podNamespace := event.PodSandboxMetadata.Namespace

	timestamp := e.clock.Now()
	status, err := e.runtime.GetPodStatus(podID, podName, podNamespace)

	if err != nil {
		// nolint:logcheck // Not using the result of klog.V inside the
		// if branch is okay, we just use it to determine whether the
		// additional "podStatus" key and its value should be added.
		if klog.V(6).Enabled() {
			klog.ErrorS(err, "PLEG: Write status", "pod", klog.KRef(podNamespace, podName), "podStatus", status)
		} else {
			klog.ErrorS(err, "PLEG: Write status", "pod", klog.KRef(podNamespace, podName))
		}
	} else {
		if klogV := klog.V(6); klogV.Enabled() {
			klogV.InfoS("PLEG: Write status", "pod", klog.KRef(podNamespace, podName), "podStatus", status)
		} else {
			klog.V(4).InfoS("PLEG: Write status", "pod", klog.KRef(podNamespace, podName))
		}
		// Preserve the pod IP across cache updates if the new IP is empty.
		// When a pod is torn down, kubelet may race with PLEG and retrieve
		// a pod status after network teardown, but the kubernetes API expects
		// the completed pod's IP to be available after the pod is dead.
		status.IPs = e.getPodIPs(podID, status)
	}

	// Generate Pod Life Cycle events using the CRI events and Pod Status
	// e.getContainerStatesFromPodStatus(status, event) // TODO - See if this helps in making the pod states more accurate
	if event.ContainerEventType == runtimeapi.ContainerEventType_CONTAINER_DELETED_EVENT {
		for _, sandbox := range status.SandboxStatuses {
			if sandbox.Id == event.ContainerId {
				e.cache.Delete(podID)
			}
		}
	} else {
		e.cache.Set(podID, status, err, timestamp)
	}
	e.cache.UpdateTime(timestamp)

	return nil
}

func generateEventsFromContainerState(state plegContainerState, event *runtimeapi.ContainerEventResponse) *PodLifecycleEvent {
	switch state {
	case plegContainerRunning:
		return &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerStarted, Data: event.ContainerId}
	case plegContainerExited:
		return &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerDied, Data: event.ContainerId}
	case plegContainerUnknown:
		return &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerChanged, Data: event.ContainerId}
	case plegContainerNonExistent:
		return &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerRemoved, Data: event.ContainerId}
	}
	return nil
}

func (e *EventedPLEG) getContainerStatesFromPodStatus(podStatus *kubecontainer.PodStatus, event *runtimeapi.ContainerEventResponse) {
	// Default to the non-existent state.
	// state := plegContainerNonExistent
	if podStatus == nil {
		// return []runtimeapi.ContainerState{state}
		e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerRemoved, Data: event.ContainerId}
	}

	// isPodRunning := true
	// allContainersDeleted := true
	for _, containerStatus := range podStatus.ContainerStatuses {
		state := convertState(containerStatus.State)
		podLifeCycleEvent := generateEventsFromContainerState(state, event)
		e.eventChannel <- podLifeCycleEvent

		// if podLifeCycleEvent.Type != ContainerStarted {
		// 	isPodRunning = false
		// }

		// if podLifeCycleEvent.Type != ContainerRemoved {
		// 	allContainersDeleted = false
		// }
	}

	for _, sandboxStatus := range podStatus.SandboxStatuses {
		if event.ContainerId == sandboxStatus.Id {

			// if isPodRunning {
			// 	e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerStarted, Data: event.ContainerId}
			// }

			// if allContainersDeleted {
			// 	e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerDied, Data: event.ContainerId}
			// 	e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerRemoved, Data: event.ContainerId}
			// }

			state := sandboxStatus.GetState()
			if state == runtimeapi.PodSandboxState_SANDBOX_READY && event.ContainerEventType == runtimeapi.ContainerEventType_CONTAINER_STARTED_EVENT {
				e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerStarted, Data: event.ContainerId}
			}

			if state == runtimeapi.PodSandboxState_SANDBOX_NOTREADY && event.ContainerEventType == runtimeapi.ContainerEventType_CONTAINER_DELETED_EVENT {
				e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerDied, Data: event.ContainerId}
				e.eventChannel <- &PodLifecycleEvent{ID: types.UID(event.PodSandboxMetadata.Uid), Type: ContainerRemoved, Data: event.ContainerId}
			}
		}
	}

}

func (e *EventedPLEG) getPodIPs(pid types.UID, status *kubecontainer.PodStatus) []string {
	if len(status.IPs) != 0 {
		return status.IPs
	}

	oldStatus, err := e.cache.Get(pid)
	if err != nil || len(oldStatus.IPs) == 0 {
		return nil
	}

	for _, sandboxStatus := range status.SandboxStatuses {
		// If at least one sandbox is ready, then use this status update's pod IP
		if sandboxStatus.State == runtimeapi.PodSandboxState_SANDBOX_READY {
			return status.IPs
		}
	}

	// For pods with no ready containers or sandboxes (like exited pods)
	// use the old status' pod IP
	return oldStatus.IPs
}
