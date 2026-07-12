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

package v1alpha1

import multinetworkv1alpha1 "github.com/kubernetes-sigs/multi-network-api/apis/v1alpha1"

// NetworkInterfaceAttribute represents the attributes of a network interface.
type NetworkInterfaceAttribute string

const (
	// NetworkInterfaceAttributesPrefix is the prefix used for network interface attributes.
	NetworkInterfaceAttributesPrefix = "devicenetwork.io"

	// NetworkInterfaceAttributesDeviceType represents the type of the network interface to be configured.
	// This is determined by the deviceType field in the DeviceConfiguration.
	// e.g. HostDevice, Macvlan...
	// The value type of this attribute is string.
	// This atttribute is always present for devices created from a DeviceNetwork.
	NetworkInterfaceAttributesDeviceType NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "deviceType"
	// NetworkInterfaceAttributesPodNetwork represents the name of the DeviceNetwork object used to configure this
	// device.
	// The value type of this attribute is string.
	// This atttribute is always present for devices created from a DeviceNetwork.
	NetworkInterfaceAttributesPodNetwork NetworkInterfaceAttribute = NetworkInterfaceAttribute(multinetworkv1alpha1.StandardDeviceAttributePodNetwork)
	// NetworkInterfaceAttributesNetworkKind represents the type of the object used to configure this device.
	// The value will always be "DeviceNetwork".
	// The value type of this attribute is string.
	// This atttribute is always present for devices created from a DeviceNetwork.
	NetworkInterfaceAttributesNetworkKind NetworkInterfaceAttribute = NetworkInterfaceAttribute(multinetworkv1alpha1.StandardDeviceAttributeNetworkKind)
	// DeviceConfiguration represents the configuration name in the DeviceNetwork
	// object used to configure this device.
	// The value type of this attribute is string.
	// This atttribute is always present for devices created from a DeviceNetwork.
	NetworkInterfaceAttributesDeviceConfiguration NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "deviceConfiguration"

	// Attributes with the prefix "hostDevice" represent the attributes of the host
	// device used to create this network interface.

	// NetworkInterfaceAttributesHostDeviceName represents the name of the host device used to create this network interface.
	// The value type of this attribute is string.
	// This atttribute is always present for devices created from a HostNetworkDevice.
	NetworkInterfaceAttributesHostDeviceName NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceName"
)

// DeviceSelectorAttribute represents the attributes of a network interface.
type DeviceSelectorAttribute string

const (
	// InterfaceNameDeviceSelectorAttribute represents the name of the network interface.
	InterfaceNameDeviceSelectorAttribute DeviceSelectorAttribute = "interfaceName"
)
