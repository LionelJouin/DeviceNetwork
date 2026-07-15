/*
Copyright (c) 2026

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
	"github.com/lioneljouin/devicenetwork/pkg/device"
	"github.com/lioneljouin/devicenetwork/pkg/resolver"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

const (
	capacity string = v1alpha1.NetworkInterfaceAttributesPrefix + "/" + "maxVirtualInterfaces"
)

type Macvlan struct {
}

// Configure creates a macvlan interface in the pod's network namespace
// based on the provided device configuration and device attributes.
func (mcvln *Macvlan) Allocate(
	ctx context.Context,
	device *resolver.Device,
) (*v1alpha1.ResourceClaimDeviceStatusData, error) {
	resourceClaimDeviceStatusData := &v1alpha1.ResourceClaimDeviceStatusData{
		Macvlan: &v1alpha1.MacvlanStatus{},
	}

	macvlanConfig := v1alpha1.GetMacvlan(*device.DeviceConfiguration)

	hostDeviceName, ok := device.ExposedDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesHostDeviceName)]
	if !ok || hostDeviceName.StringValue == nil {
		return nil, fmt.Errorf("device %q does not have the %q attribute", device.ExposedDevice.Name, resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesHostDeviceName))
	}

	resourceClaimDeviceStatusData.Macvlan.ParentName = *hostDeviceName.StringValue

	hostDevice, err := netlink.LinkByName(*hostDeviceName.StringValue)
	if err != nil {
		return nil, fmt.Errorf("failed to get link by name %q: %v", *hostDeviceName.StringValue, err)
	}
	resourceClaimDeviceStatusData.Macvlan.ParentIndex = hostDevice.Attrs().Index

	switch *macvlanConfig.Mode {
	case v1alpha1.MacvlanModeBridge:
		resourceClaimDeviceStatusData.Macvlan.Mode = int(netlink.MACVLAN_MODE_BRIDGE)
	case v1alpha1.MacvlanModePrivate:
		resourceClaimDeviceStatusData.Macvlan.Mode = int(netlink.MACVLAN_MODE_PRIVATE)
	case v1alpha1.MacvlanModeVepa:
		resourceClaimDeviceStatusData.Macvlan.Mode = int(netlink.MACVLAN_MODE_VEPA)
	case v1alpha1.MacvlanModePassthru:
		resourceClaimDeviceStatusData.Macvlan.Mode = int(netlink.MACVLAN_MODE_PASSTHRU)
	case v1alpha1.MacvlanModeSource:
		resourceClaimDeviceStatusData.Macvlan.Mode = int(netlink.MACVLAN_MODE_SOURCE)
	default:
		return nil, fmt.Errorf("invalid macvlan mode %q", macvlanConfig.Mode)
	}

	return resourceClaimDeviceStatusData, nil
}

// Configure creates a macvlan interface in the pod's network namespace
// based on the provided device configuration and device attributes.
func (mcvln *Macvlan) Configure(
	ctx context.Context,
	podNetworkNamespace string,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) error {
	if allocatedDeviceStatus == nil {
		return fmt.Errorf("allocated device status is nil")
	}

	if allocatedDeviceStatus.NetworkData == nil {
		return fmt.Errorf("allocated device status does not contain network data")
	}

	resourceClaimDeviceStatusData := &v1alpha1.ResourceClaimDeviceStatusData{}
	err := json.Unmarshal(allocatedDeviceStatus.Data.Raw, resourceClaimDeviceStatusData)
	if err != nil {
		return fmt.Errorf("failed to unmarshal allocated device status data: %v", err)
	}

	if resourceClaimDeviceStatusData.Macvlan == nil {
		return fmt.Errorf("allocated device status data does not contain macvlan information")
	}

	nsHandle, err := netns.GetFromPath(podNetworkNamespace)
	if err != nil {
		return fmt.Errorf("failed to get network namespace from path %q: %v", podNetworkNamespace, err)
	}
	defer nsHandle.Close()

	linkAttrs := netlink.NewLinkAttrs()
	linkAttrs.Name = allocatedDeviceStatus.NetworkData.InterfaceName
	linkAttrs.ParentIndex = resourceClaimDeviceStatusData.Macvlan.ParentIndex
	linkAttrs.Namespace = netlink.NsFd(nsHandle)
	macvlan := netlink.Macvlan{
		LinkAttrs: linkAttrs,
		Mode:      netlink.MacvlanMode(resourceClaimDeviceStatusData.Macvlan.Mode),
	}

	err = netlink.LinkAdd(&macvlan)
	if err != nil {
		return fmt.Errorf("failed to add macvlan link: %v", err)
	}

	return nil
}

// ConfigureExposedDevice creates a device object representing the
// macvlan interface to be exposed in resource slice.
func (mcvln *Macvlan) ConfigureExposedDevice(
	ctx context.Context,
	deviceConfiguration v1alpha1.DeviceConfiguration,
	device *device.Device,
) *resourcev1.Device {
	if !mcvln.applicableDevice(ctx, deviceConfiguration, device) {
		return nil
	}

	one := resource.MustParse("1")
	maxVirtualDevices := resource.MustParse("65535")

	return &resourcev1.Device{
		Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
		Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{
			resourcev1.QualifiedName(capacity): {
				Value: maxVirtualDevices,
				RequestPolicy: &resourcev1.CapacityRequestPolicy{
					Default:     &one,
					ValidValues: []resource.Quantity{one},
				},
			},
		},
		AllowMultipleAllocations: ptr.To(true),
	}
}

func (mcvln *Macvlan) applicableDevice(
	ctx context.Context,
	deviceConfiguration v1alpha1.DeviceConfiguration,
	device *device.Device,
) bool {
	if device == nil || v1alpha1.GetDeviceType(deviceConfiguration) != v1alpha1.DeviceTypeMacvlan {
		return false
	}

	return true
}
