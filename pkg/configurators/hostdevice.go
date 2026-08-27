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

package configurators

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	"github.com/lioneljouin/devicenetwork/pkg/status"
	"github.com/vishvananda/netlink"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

type HostDevice struct {
}

// IsSupported reports whether the given host device can be configured
// according to the given DeviceConfiguration.
func (hd *HostDevice) IsSupported(
	ctx context.Context,
	hostDevice *host.Device,
	deviceConfiguration *v1alpha1.DeviceConfiguration,
) (bool, error) {
	if hostDevice == nil {
		return false, fmt.Errorf("hostDevice is nil")
	}

	if deviceConfiguration == nil {
		return false, fmt.Errorf("deviceConfiguration is nil")
	}

	if v1alpha1.GetDeviceType(*deviceConfiguration) != v1alpha1.DeviceTypeHostDevice {
		return false, nil
	}

	link, err := netlink.LinkByName(hostDevice.Spec.InterfaceName)
	if err != nil {
		return false, fmt.Errorf("failed to get link %q: %v", hostDevice.Spec.InterfaceName, err)
	}

	// The loopback device cannot be moved between network namespaces; every
	// namespace already has its own "lo" that the kernel refuses to replace.
	if link.Attrs().EncapType == "loopback" {
		return false, nil
	}

	// Bridge, bond, and team devices carry NETIF_F_NETNS_LOCAL: the kernel
	// refuses to move them to another namespace at all, regardless of whether
	// they currently have any enslaved interfaces.
	switch link.Type() {
	case "bridge", "bond", "team":
		return false, nil
	}

	// A device already enslaved to a bridge or bond must be released from its
	// master before it can be handed over to a pod's network namespace.
	if link.Attrs().MasterIndex != 0 {
		return false, nil
	}

	return true, nil
}

// ExposedDevice configures the device which will be exposed in ResourceSlice.
func (hd *HostDevice) ExposedDevice(
	ctx context.Context,
	hostDevice *host.Device,
	device *resourcev1.Device,
) (*resourcev1.Device, error) {
	deviceRes := &resourcev1.Device{}
	if device != nil {
		deviceRes = device.DeepCopy()
		if deviceRes.Attributes == nil {
			deviceRes.Attributes = map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{}
		}
		if deviceRes.Capacity == nil {
			deviceRes.Capacity = map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{}
		}
		if deviceRes.ConsumesCounters == nil {
			deviceRes.ConsumesCounters = []resourcev1.DeviceCounterConsumption{}
		}
	} else {
		deviceRes = &resourcev1.Device{
			Attributes:       map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
			Capacity:         map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{},
			ConsumesCounters: []resourcev1.DeviceCounterConsumption{},
		}
	}

	if hostDevice == nil {
		return nil, fmt.Errorf("hostDevice is nil")
	}

	max := resource.MustParse("65536")

	deviceRes.AllowMultipleAllocations = ptr.To(false)

	deviceRes.ConsumesCounters = append(deviceRes.ConsumesCounters, resourcev1.DeviceCounterConsumption{
		CounterSet: hostDevice.Name,
		Counters: map[string]resourcev1.Counter{
			"mutual-exclusion": {Value: max},
		},
	})

	return deviceRes, nil
}

// Allocate allocates the network device by gathering the necessary information
// and storing it in the ResourceClaim Device Status.
//
// The necessary information includes the interface name, parent device name, IPs
// and other relevant information to configure the network device.
func (hd *HostDevice) Allocate(
	ctx context.Context,
	hostDevice *host.Device,
	deviceConfiguration *v1alpha1.DeviceConfiguration,
	networkInterfaceConfiguration *v1alpha1.NetworkInterfaceConfiguration,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	if allocatedDeviceStatus == nil {
		return nil, fmt.Errorf("allocatedDeviceStatus is nil")
	}
	allocatedDeviceStatusRes := allocatedDeviceStatus.DeepCopy()

	if deviceConfiguration == nil {
		return nil, fmt.Errorf("deviceConfiguration is nil")
	}

	if hostDevice == nil {
		return nil, fmt.Errorf("hostDevice is nil")
	}

	// if network data is nil, initialize it with a new NetworkDeviceData and set the InterfaceName to a random name.
	if allocatedDeviceStatusRes.NetworkData == nil {
		allocatedDeviceStatusRes.NetworkData = &resourcev1.NetworkDeviceData{
			InterfaceName: hostDevice.Spec.InterfaceName,
		}
	}

	resourceClaimDeviceStatusData := &status.ResourceClaimDeviceStatusData{}
	if allocatedDeviceStatusRes.Data != nil && allocatedDeviceStatusRes.Data.Raw != nil {
		err := json.Unmarshal(allocatedDeviceStatusRes.Data.Raw, resourceClaimDeviceStatusData)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal allocated device status data: %v", err)
		}
	}

	resourceClaimDeviceStatusData.Device = hostDevice.DeepCopy()
	resourceClaimDeviceStatusData.DeviceConfiguration = deviceConfiguration.DeepCopy()

	resultBytes, err := json.Marshal(resourceClaimDeviceStatusData)
	if err != nil {
		return nil, fmt.Errorf("failed to json.Marshal result (%v): %v", resourceClaimDeviceStatusData, err)
	}

	allocatedDeviceStatusRes.Data = &runtime.RawExtension{
		Raw: resultBytes,
	}

	return allocatedDeviceStatusRes, nil
}

// Configure configures the device.
//
// It must be called when the pod is getting created and after the ResourceClaim is allocated.
func (hd *HostDevice) Configure(
	ctx context.Context,
	podNetworkNamespace string,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}

// Release releases the device.
//
// It must be called when the Pod is getting deleted.
func (hd *HostDevice) Release(
	ctx context.Context,
	podNetworkNamespace string,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}
