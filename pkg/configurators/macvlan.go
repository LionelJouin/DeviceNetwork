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
	"fmt"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/device"
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

func (mcvln *Macvlan) Configure(
	ctx context.Context,
	podNetworkNamespace string,
	deviceConfiguration *v1alpha1.DeviceConfiguration,
	device *resourcev1.Device,
) error {
	macvlanConfig := v1alpha1.GetMacvlan(*deviceConfiguration)

	hostDeviceName, ok := device.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesHostDeviceName)]
	if !ok || hostDeviceName.StringValue == nil {
		return fmt.Errorf("device %q does not have the %q attribute", device.Name, resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesHostDeviceName))
	}

	hostDevice, err := netlink.LinkByName(*hostDeviceName.StringValue)
	if err != nil {
		return fmt.Errorf("failed to get link by name %q: %v", *hostDeviceName.StringValue, err)
	}

	nsHandle, err := netns.GetFromPath(podNetworkNamespace)
	if err != nil {
		return fmt.Errorf("failed to get network namespace from path %q: %v", podNetworkNamespace, err)
	}
	defer nsHandle.Close()

	linkAttrs := netlink.NewLinkAttrs()
	linkAttrs.Name = randomName()
	linkAttrs.ParentIndex = hostDevice.Attrs().Index
	linkAttrs.Namespace = netlink.NsFd(nsHandle)
	macvlan := netlink.Macvlan{
		LinkAttrs: linkAttrs,
	}

	switch *macvlanConfig.Mode {
	case v1alpha1.MacvlanModeBridge:
		macvlan.Mode = netlink.MACVLAN_MODE_BRIDGE
	case v1alpha1.MacvlanModePrivate:
		macvlan.Mode = netlink.MACVLAN_MODE_PRIVATE
	case v1alpha1.MacvlanModeVepa:
		macvlan.Mode = netlink.MACVLAN_MODE_VEPA
	case v1alpha1.MacvlanModePassthru:
		macvlan.Mode = netlink.MACVLAN_MODE_PASSTHRU
	case v1alpha1.MacvlanModeSource:
		macvlan.Mode = netlink.MACVLAN_MODE_SOURCE
	default:
		return fmt.Errorf("invalid macvlan mode %q", macvlanConfig.Mode)
	}

	err = netlink.LinkAdd(&macvlan)
	if err != nil {
		return fmt.Errorf("failed to add macvlan link: %v", err)
	}

	return nil
}

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
