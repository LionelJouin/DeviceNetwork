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

package v1alpha1

import (
	multinetworkv1alpha1 "github.com/kubernetes-sigs/multi-network-api/apis/v1alpha1"
)

// NetworkInterfaceAttribute represents the attributes of a network interface.
type NetworkInterfaceAttribute string

// NetworkInterfaceCapacity represents the capacities of a network interface.
type NetworkInterfaceCapacity string

const (
	// NetworkInterfaceAttributePrefix is the prefix used for network interface attributes.
	NetworkInterfaceAttributePrefix = "devicenetwork.io"

	// NetworkInterfaceAttributeDeviceType represents the type of the network interface to be configured.
	// This is determined by the deviceType field in the DeviceConfiguration.
	// e.g. HostDevice, Macvlan...
	// The value type of this attribute is string.
	// This attribute is always present for devices created from a DeviceNetwork.
	NetworkInterfaceAttributeDeviceType NetworkInterfaceAttribute = NetworkInterfaceAttributePrefix + "/" + "deviceType"
	// NetworkInterfaceAttributePodNetwork represents the name of the DeviceNetwork object used to configure this
	// device.
	// The value type of this attribute is string.
	// This attribute is always present for devices created from a DeviceNetwork.
	NetworkInterfaceAttributePodNetwork NetworkInterfaceAttribute = NetworkInterfaceAttribute(multinetworkv1alpha1.StandardDeviceAttributePodNetwork)
	// NetworkInterfaceAttributeNetworkKind represents the type of the object used to configure this device.
	// The value will always be "DeviceNetwork".
	// The value type of this attribute is string.
	// This attribute is always present for devices created from a DeviceNetwork.
	NetworkInterfaceAttributeNetworkKind NetworkInterfaceAttribute = NetworkInterfaceAttribute(multinetworkv1alpha1.StandardDeviceAttributeNetworkKind)
	// DeviceConfiguration represents the configuration name in the DeviceNetwork
	// object used to configure this device.
	// The value type of this attribute is string.
	// This attribute is always present for devices created from a DeviceNetwork.
	NetworkInterfaceAttributeDeviceConfiguration NetworkInterfaceAttribute = NetworkInterfaceAttributePrefix + "/" + "deviceConfiguration"

	// Attributes with the prefix "hostDevice" represent the attributes of the host
	// device used to create this network interface.

	// NetworkInterfaceAttributeHostDeviceName represents the name of the host device used to create this network interface.
	// The value type of this attribute is string.
	// This attribute is always present for devices created from a HostNetworkDevice.
	NetworkInterfaceAttributeHostDeviceName NetworkInterfaceAttribute = NetworkInterfaceAttributePrefix + "/" + "hostDeviceName"

	// NetworkInterfaceAttributeMaxVirtualInterfaces represents the maximum number of virtual interfaces that can be
	// created based on a single host device.
	// The value type of this attribute is int64.
	// This capacity attribute is always present for virtual devices created from a HostNetworkDevice.
	NetworkInterfaceCapacityMaxVirtualInterfaces NetworkInterfaceCapacity = NetworkInterfaceAttributePrefix + "/" + "maxVirtualInterfaces"

	// MaxVirtualDevices is the maximum number of macvlan devices that can be created
	// on a single parent device.
	// This constant is used to set the value of the NetworkInterfaceCapacityMaxVirtualInterfaces attribute.
	MaxVirtualDevices int64 = 65535
)

// DeviceSelectorAttribute represents the attributes of a network interface.
type DeviceSelectorAttribute string

const (
	// InterfaceNameDeviceSelectorAttribute represents the name of the network interface.
	InterfaceNameDeviceSelectorAttribute DeviceSelectorAttribute = "interfaceName"
)
