/*
Copyright 2026 The Kubernetes Authors.

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

package nri

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/containerd/nri/pkg/api"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/klog/v2"
)

type configurationProcess struct {
	pod                 *api.PodSandbox
	podNetworkNamespace string
	claims              []*resourcev1.ResourceClaim
	driverName          string
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator

	// mu guards done and cancel, which are written by run() and read by
	// isDone() and terminate(). run() executes in its own goroutine (started
	// from RunPodSandbox), while isDone() and terminate() are called from the
	// NRI stub's dispatch goroutine (StartContainer, RemovePodSandbox), so
	// these fields are accessed concurrently.
	mu sync.Mutex
	// done is closed once the background goroutine started in RunPodSandbox
	// has finished configuring all claims.
	done   chan struct{}
	cancel context.CancelFunc
}

func newConfigurationProcess(
	pod *api.PodSandbox,
	podNetworkNamespace string,
	claims []*resourcev1.ResourceClaim,
	driverName string,
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator,
) *configurationProcess {
	return &configurationProcess{
		pod:                 pod,
		podNetworkNamespace: podNetworkNamespace,
		claims:              claims,
		driverName:          driverName,
		deviceConfigurators: deviceConfigurators,
	}
}

// isDone checks if the configuration process has finished.
func (cp *configurationProcess) isDone() bool {
	cp.mu.Lock()
	done := cp.done
	cp.mu.Unlock()

	if done == nil {
		return true
	}

	select {
	case <-done:
		return true
	default:
		return false
	}
}

// terminate stops the configuration process if it is running and waits for it to finish.
func (cp *configurationProcess) terminate() {
	cp.mu.Lock()
	cancel := cp.cancel
	done := cp.done
	cp.cancel = nil
	cp.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

// run starts the configuration process. It configures each resource claim associated with the pod.
// It should be called in a separate goroutine.
func (cp *configurationProcess) run(ctx context.Context) {
	// Stop and wait for any previous run before starting a new one.
	cp.terminate()

	configCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	cp.mu.Lock()
	cp.cancel = cancel
	cp.done = done
	cp.mu.Unlock()

	defer func() {
		close(done)
		cp.terminate()
	}()

	for _, claim := range cp.claims {
		err := cp.configureResourceClaim(configCtx, claim)
		if err != nil {
			klog.FromContext(ctx).Error(err, "failed to configure resource claim", "claim", claim.Name)
			// Handle the error appropriately, e.g., log it or return it.
		}
	}
}

// configureResourceClaim configures the resources for a given resource claim.
// It iterates over the devices in the claim and uses the appropriate configurator based on the device type.
func (cp *configurationProcess) configureResourceClaim(ctx context.Context, claim *resourcev1.ResourceClaim) error {
	for _, deviceStatus := range claim.Status.Devices {
		if deviceStatus.Driver != cp.driverName {
			continue
		}

		resourceClaimDeviceStatusData := &v1alpha1.ResourceClaimDeviceStatusData{}
		err := json.Unmarshal(deviceStatus.Data.Raw, resourceClaimDeviceStatusData)
		if err != nil {
			return fmt.Errorf("failed to unmarshal allocated device status data: %v", err)
		}

		if resourceClaimDeviceStatusData.DeviceType == nil {
			return fmt.Errorf("device type is nil in resource claim device status data")
		}

		configuration, exists := cp.deviceConfigurators[*resourceClaimDeviceStatusData.DeviceType]
		if !exists {
			return fmt.Errorf("no configurator found for device type: %v", *resourceClaimDeviceStatusData.DeviceType)
		}

		_, err = configuration.Configure(ctx, cp.podNetworkNamespace, &deviceStatus)
		if err != nil {
			return fmt.Errorf("failed to configure device: %v", err)
		}

		// Implement the logic to configure the device based on the claim and driver.
		// This might involve interacting with the device plugin, setting up the device, etc.
		// For now, we'll just simulate a successful configuration.
	}
	return nil
}
