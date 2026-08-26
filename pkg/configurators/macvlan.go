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
	"github.com/vishvananda/netns"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

type Macvlan struct {
	CommonConfigurator *CommonConfigurator
}

var macvlanModeMap = map[v1alpha1.MacvlanMode]netlink.MacvlanMode{
	v1alpha1.MacvlanModeBridge:   netlink.MACVLAN_MODE_BRIDGE,
	v1alpha1.MacvlanModePrivate:  netlink.MACVLAN_MODE_PRIVATE,
	v1alpha1.MacvlanModeVepa:     netlink.MACVLAN_MODE_VEPA,
	v1alpha1.MacvlanModePassthru: netlink.MACVLAN_MODE_PASSTHRU,
	v1alpha1.MacvlanModeSource:   netlink.MACVLAN_MODE_SOURCE,
}

// Allocate allocates the network device by gathering the necessary information
// and storing it in the ResourceClaim Device Status.
//
// The necessary information includes the interface name, parent device name, IPs
// and other relevant information to configure the network device.
func (mcvln *Macvlan) Allocate(
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
			InterfaceName: randomName(),
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

	if mcvln.CommonConfigurator != nil {
		allocatedDeviceStatusRes, err = mcvln.CommonConfigurator.Allocate(ctx, hostDevice, networkInterfaceConfiguration, allocatedDeviceStatusRes)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate common network data: %v", err)
		}
	}

	return allocatedDeviceStatusRes, nil
}

// ExposedDevice configures the device which will be exposed in ResourceSlice.
func (mcvln *Macvlan) ExposedDevice(
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

	one := resource.MustParse("1")
	maxVirtualDevices := resource.MustParse(fmt.Sprintf("%d", v1alpha1.MaxVirtualDevices))

	deviceRes.AllowMultipleAllocations = ptr.To(true)
	deviceRes.Capacity[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceCapacityMaxVirtualInterfaces)] = resourcev1.DeviceCapacity{
		Value: maxVirtualDevices,
		RequestPolicy: &resourcev1.CapacityRequestPolicy{
			Default:     &one,
			ValidValues: []resource.Quantity{one},
		},
	}

	deviceRes.ConsumesCounters = append(deviceRes.ConsumesCounters, resourcev1.DeviceCounterConsumption{
		CounterSet: hostDevice.Name,
		Counters: map[string]resourcev1.Counter{
			"mutual-exclusion": {Value: one},
		},
	})

	return deviceRes, nil
}

// Configure configures the device.
//
// It must be called when the pod is getting created and after the ResourceClaim is allocated.
func (mcvln *Macvlan) Configure(
	ctx context.Context,
	podNetworkNamespace string,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	if allocatedDeviceStatus == nil {
		return nil, fmt.Errorf("allocatedDeviceStatus is nil")
	}
	allocatedDeviceStatusRes := allocatedDeviceStatus.DeepCopy()

	resourceClaimDeviceStatusData := &status.ResourceClaimDeviceStatusData{}
	err := json.Unmarshal(allocatedDeviceStatus.Data.Raw, resourceClaimDeviceStatusData)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal allocated device status data: %v", err)
	}

	if resourceClaimDeviceStatusData.Device == nil {
		return nil, fmt.Errorf("allocated device status data does not contain device information")
	}

	if resourceClaimDeviceStatusData.DeviceConfiguration == nil {
		return nil, fmt.Errorf("allocated device status data does not contain device configuration information")
	}

	if v1alpha1.GetDeviceType(*resourceClaimDeviceStatusData.DeviceConfiguration) != v1alpha1.DeviceTypeMacvlan {
		return nil, fmt.Errorf("allocated device status data does not contain macvlan device configuration information")
	}

	nsHandle, err := netns.GetFromPath(podNetworkNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get network namespace from path %q: %v", podNetworkNamespace, err)
	}
	defer func() {
		if err := nsHandle.Close(); err != nil {
			klog.FromContext(ctx).Error(err, "failed to close network namespace handle")
		}
	}()

	// hwAddr, err := net.ParseMAC(allocatedDeviceStatus.NetworkData.HardwareAddress)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to parse hardware address %q: %v", allocatedDeviceStatus.NetworkData.HardwareAddress, err)
	// }

	deviceConfiguration := resourceClaimDeviceStatusData.DeviceConfiguration
	macvlanConfig := v1alpha1.GetMacvlan(*deviceConfiguration)

	mode, ok := macvlanModeMap[*macvlanConfig.Mode]
	if !ok {
		return nil, fmt.Errorf("invalid macvlan mode %q", *macvlanConfig.Mode)
	}

	linkAttrs := netlink.NewLinkAttrs()
	linkAttrs.Name = allocatedDeviceStatus.NetworkData.InterfaceName
	// linkAttrs.HardwareAddr = hwAddr
	linkAttrs.ParentIndex = resourceClaimDeviceStatusData.Device.Spec.InterfaceIndex
	linkAttrs.Namespace = netlink.NsFd(nsHandle)
	macvlan := netlink.Macvlan{
		LinkAttrs: linkAttrs,
		Mode:      mode,
	}

	err = netlink.LinkAdd(&macvlan)
	if err != nil {
		return nil, fmt.Errorf("failed to add macvlan link: %v", err)
	}

	return allocatedDeviceStatusRes, nil
}

// Release releases the device.
//
// It must be called when the Pod is getting deleted.
func (mcvln *Macvlan) Release(
	ctx context.Context,
	podNetworkNamespace string,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	return allocatedDeviceStatus, nil
}

// IsSupported reports whether the given host device can be configured
// according to the given DeviceConfiguration.
func (mcvln *Macvlan) IsSupported(
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

	if v1alpha1.GetDeviceType(*deviceConfiguration) != v1alpha1.DeviceTypeMacvlan {
		return false, nil
	}

	macvlanConfig := v1alpha1.GetMacvlan(*deviceConfiguration)
	if _, ok := macvlanModeMap[*macvlanConfig.Mode]; !ok {
		return false, nil
	}

	link, err := netlink.LinkByName(hostDevice.Spec.InterfaceName)
	if err != nil {
		return false, fmt.Errorf("failed to get link %q: %v", hostDevice.Spec.InterfaceName, err)
	}

	// The kernel only allows creating a macvlan on top of an Ethernet-type
	// lower device; it rejects (EINVAL) non-Ethernet devices such as loopback or tunnels.
	if link.Attrs().EncapType != "ether" {
		return false, nil
	}

	// A device already enslaved to a bridge or bond has an rx_handler registered
	// by its master; the kernel only allows one rx_handler per device, so adding
	// a macvlan on top of it fails with EBUSY.
	if link.Attrs().MasterIndex != 0 {
		return false, nil
	}

	return true, nil
}
