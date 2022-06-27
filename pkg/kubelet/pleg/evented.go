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
	// The channel from which the subscriber listens events.
	eventChannel chan *PodLifecycleEvent
	// The internal cache for pod/container information.
	podRecords podRecords
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
func NewEventedPLEG(runtime kubecontainer.Runtime, runtimeService internalapi.RuntimeService, channelCapacity int,
	relistPeriod time.Duration, cache kubecontainer.Cache, clock clock.Clock) PodLifecycleEventGenerator {
	return &EventedPLEG{
		relistPeriod:   relistPeriod,
		runtime:        runtime,
		runtimeService: runtimeService,
		eventChannel:   make(chan *PodLifecycleEvent, channelCapacity),
		podRecords:     make(podRecords),
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
func (g *EventedPLEG) Healthy() (bool, error) {
	return true, nil
}

func (e *EventedPLEG) watchEventsChannel() {

	containerEventsResponseCh := make(chan *runtimeapi.ContainerEventResponse, 1000)
	defer close(containerEventsResponseCh)
	go e.runtimeService.GetContainerEvents(containerEventsResponseCh)

	// plegCh := kl.pleg.Watch()

	for event := range containerEventsResponseCh {
		switch event.ContainerEventType {
		case runtimeapi.ContainerEventType_CONTAINER_STOPPED_EVENT:
			klog.V(2).InfoS("Recieved Container Stopped Event", "CRI container event", event)
			// Push the event to PLEG channel
			// plegCh <- &pleg.PodLifecycleEvent{ID: types.UID(event.SandboxId), Type: pleg.ContainerDied, Data: event.ContainerId}
		case runtimeapi.ContainerEventType_CONTAINER_CREATED_EVENT:
			// Push the event to PLEG channel
			// plegCh <- &pleg.PodLifecycleEvent{ID: types.UID(event.SandboxId), Type: pleg.ContainerCreated, Data: event.ContainerId}
			klog.V(2).InfoS("Recieved Container Created Event", "CRI container event", event)
		case runtimeapi.ContainerEventType_CONTAINER_STARTED_EVENT:
			// Push the event to PLEG channel
			// plegCh <- &pleg.PodLifecycleEvent{ID: types.UID(event.SandboxId), Type: pleg.ContainerStarted, Data: event.ContainerId}
			klog.V(2).InfoS("Recieved Container Started Event", "CRI container event", event)
		}
	}
}
