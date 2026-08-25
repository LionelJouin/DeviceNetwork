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

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:resource:shortName=hnd,scope=Cluster,categories={podnetwork,podnetworks,all}
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DeviceNetwork is a specification for a DeviceNetwork resource.
type DeviceNetwork struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired behavior of the DeviceNetwork.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	// +optional
	Spec DeviceNetworkSpec `json:"spec"`

	// Most recently observed status of the DeviceNetwork.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	// +optional
	Status DeviceNetworkStatus `json:"status"`
}

const (
	// DeviceSelectorMaxSize is the maximum number of DeviceSelectors in a DeviceNetwork.
	// This limit could be increased in the future based on the actual needs.
	DeviceSelectorMaxSize = 4
	// DeviceConfigurationMaxSize is the maximum number of DeviceConfigurations in a DeviceNetwork.
	// This limit could be increased in the future based on the actual needs.
	DeviceConfigurationMaxSize = 4
	// SelectorPerDeviceConfigurationMaxSize is the maximum number of DeviceSelectors that can be referenced by a DeviceConfiguration.
	// This limit could be increased in the future based on the actual needs.
	SelectorPerDeviceConfigurationMaxSize = DeviceSelectorMaxSize
	// SelectorPerDeviceSelectorMaxSize is the maximum number of Selectors that can be specified in a DeviceSelector.
	// This limit could be increased in the future based on the actual needs.
	SelectorPerDeviceSelectorMaxSize = 1
)

// DeviceNetworkSpec is the spec for a DeviceNetwork resource.
// DeviceNetwork represents a set of device networks which are
// selected by the DeviceSelector and configured by the DeviceConfiguration.
//
// The DeviceNetwork consists of three main components (DeviceSelectors,
// DeviceConfiguration and NetworkInterfaceConfiguration) chained together
// to represent a set of network interfaces.
//
// Example:
// 1. A network device with the interface name "eno1np0" is selected by a
// DeviceSelector on "node-a".
//
// 2. The selected device "eno1np0" will be exposed as a new device in a
// ResourceSlice on the node "node-a" with its "name.<DeviceNetwork.Name>.<DeviceConfiguration.Name>"
// and the related attributes.
//
// 3. When the device is claimed by a Pod, the device will be configured according to the
// DeviceConfiguration and the NetworkInterfaceConfiguration. For example, if the
// DeviceConfiguration specifies a Macvlan configuration, then a Macvlan interface will
// be created on top of "eno1np0" and the created Macvlan interface will be configured
// in the Pod.
type DeviceNetworkSpec struct {
	// DeviceSelectors selects network devices on
	// specific nodes based on the specified selection criteria.
	//
	// The maximum number of DeviceSelectors is 4. This limit could be increased
	// in the future based on the actual needs.
	//
	// +required
	DeviceSelectors []DeviceSelector `json:"deviceSelectors,omitempty"`

	// DeviceConfigurations defines the configuration for
	// network devices selected by the DeviceSelector.
	//
	// The maximum number of DeviceConfigurations is 4. This limit could be increased
	// in the future based on the actual needs.
	//
	// +required
	DeviceConfigurations []DeviceConfiguration `json:"deviceConfigurations,omitempty"`

	// NetworkInterfaceConfiguration defines the configuration for the network interface
	// created based on the selected network device and the DeviceConfiguration.
	// The configuration will be applied to all the network interfaces created based
	// on the selected network device and the DeviceConfiguration.
	//
	// +optional
	NetworkInterfaceConfiguration NetworkInterfaceConfiguration `json:"networkInterfaceConfiguration,omitempty"`
}

// DeviceNetworkStatus is the status for a DeviceNetwork resource.
type DeviceNetworkStatus struct {
}

// DeviceSelector represents the selection criteria for selecting network
// devices on specific nodes.
// A DeviceSelector must be referenced by at least one DeviceConfiguration and can
// be referenced by multiple DeviceConfigurations.
type DeviceSelector struct {
	// Name is the identifier for this selection. The name is used to reference
	// this selection from the DeviceConfigurations.
	//
	// +required
	Name string `json:"name,omitempty"`

	// Selectors defines the selection criteria for selecting device networks.
	// Each selector must be satisfied for a device network to be selected by
	// this DeviceSelector.
	// If no selectors are specified, then this selection matches all devices.
	//
	// The maximum number of selectors is 1. This limit could be increased in the future
	// based on the actual needs.
	//
	// +optional
	// +listType=atomic
	Selectors []Selector `json:"selectors,omitempty"`

	// NodeSelector defines on which nodes the selectors will be applied.
	// If not specified, the selectors will be applied on all nodes.
	//
	// +optional
	NodeSelector *v1.NodeSelector `json:"nodeSelector,omitempty"`
}

// DeviceSelector must have exactly one field set.
type Selector struct {
	// CEL contains a CEL expression for selecting a device.
	//
	// +optional
	// +oneOf=SelectorType
	CEL *CELDeviceSelector `json:"cel,omitempty" protobuf:"bytes,1,opt,name=cel"`
}

// CELSelectorExpressionMaxLength is the maximum length of a CEL selector expression string.
const CELSelectorExpressionMaxLength = 10 * 1024

// CELDeviceSelector contains a CEL expression for selecting a network device.
type CELDeviceSelector struct {
	// Expression is a CEL expression which evaluates network devices.
	// It must evaluate to true when the devices under consideration satisfies
	// the desired criteria, and false when it does not.
	//
	// The expression's input is an object named "device", which carries
	// the following properties:
	//  - attributes (map[string]object): the device's attributes.
	//  - capacity (map[string]object): the device's capacities.
	//
	// Example: Consider a set of network devices which are rdma capable and
	// that are infiniBand devices. These devices would have the following attributes:
	//
	//     device.rdmaCapable = true && device.linkType = "infiniband"
	//
	// A robust expression should check for the existence of attributes
	// before referencing them.
	//
	// The length of the expression must be smaller or equal to 10 Ki. The
	// cost of evaluating it is also limited based on the estimated number
	// of logical steps.
	//
	// +required
	Expression string `json:"expression" protobuf:"bytes,1,name=expression"`
}

// DeviceConfiguration defines the configuration for network
// devices selected by the DeviceSelector.
//
// Based on the DeviceType, the network devices selected by the DeviceSelector
// which do not support the specified DeviceType will be filtered out and will
// not be considered.
// For example, if the DeviceType is "vlan", then the selected network devices
// which are already configured as "vlan" interface will not be considered as Linux
// does not allow creating a vlan interface on top of a device which is already configured
// as vlan.
type DeviceConfiguration struct {
	// Name is an identifier for this configuration.
	// It can be used to reference this configuration from other resources.
	//
	// +required
	Name string `json:"name,omitempty"`

	// DeviceSelectors defines the selections for network
	// devices to which this configuration will be applied.
	// All the selected network devices will be configured according to this configuration.
	// This field is required and must reference at least one DeviceSelector.
	// A DeviceSelector cannot be referenced multiple times by the same DeviceConfiguration.
	//
	// +required
	DeviceSelectors []string `json:"deviceSelectors,omitempty"`

	// DeviceType defines how the selected network device
	// will be configured in the Pod.
	// If not specified, the default value is "HostDevice".
	//
	// +optional
	DeviceType *DeviceType `json:"deviceType,omitempty"`

	// Macvlan is the configuration for Macvlan device type.
	// This field can be set only when DeviceType is "Macvlan".
	//
	// +optional
	Macvlan *Macvlan `json:"macvlan,omitempty"`

	// HostDevice is the configuration for HostDevice device type.
	// This field can be set only when DeviceType is "HostDevice".
	//
	// +optional
	HostDevice *HostDevice `json:"hostDevice,omitempty"`
}

// DeviceType represents the type of configuration for
// the selected host network device.
//
// +enum
type DeviceType string

const (
	// DeviceTypeHostDevice indicates that the selected
	// network device will be moved to the pod's network namespace
	// without any additional configuration.
	DeviceTypeHostDevice DeviceType = "HostDevice"

	// DeviceTypeMacvlan indicates that a Macvlan interface will be
	//  created on top of the selected network device.
	DeviceTypeMacvlan DeviceType = "Macvlan"
)

// HostDevice represents the configuration for HostDevice device type.
type HostDevice struct {
}

// MacvlanMode represents the mode of the created Macvlan interface.
// +enum
type MacvlanMode string

const (
	// MacvlanModePrivate indicates that the created Macvlan interface
	// will be created with mode private.
	MacvlanModePrivate MacvlanMode = "private"

	// MacvlanModeVepa indicates that the created Macvlan interface
	//  will be created with mode vepa.
	MacvlanModeVepa MacvlanMode = "vepa"

	// MacvlanModeBridge indicates that the created Macvlan interface
	// will be created with mode bridge.
	MacvlanModeBridge MacvlanMode = "bridge"

	// MacvlanModePassthru indicates that the created Macvlan interface
	// will be created with mode passthru.
	MacvlanModePassthru MacvlanMode = "passthru"

	// MacvlanModeSource indicates that the created Macvlan interface
	// will be created with mode source.
	MacvlanModeSource MacvlanMode = "source"
)

// Macvlan represents the configuration for Macvlan device type.
type Macvlan struct {
	// Mode is the mode of the created Macvlan interface. If not specified,
	//  the default value is "bridge".
	// +optional
	Mode *MacvlanMode `json:"mode,omitempty"`
}

// NetworkInterfaceConfiguration represents the configuration for a network interface.
type NetworkInterfaceConfiguration struct {
	// IPAM represents the IP address management configuration for the created
	// network interface.
	// +optional
	IPAM []*IPAM `json:"ipam,omitempty"`
}

// IPAMProvider represents the provider of IP address management for the created network interface.
// +enum
type IPAMProvider string

const (
	// IPAMProviderDHCP indicates that the IP address will be assigned via DHCP.
	IPAMProviderDHCP IPAMProvider = "DHCP"
	// IPAMProviderRandom indicates that the IP address will be assigned randomly,
	// this provider is used for testing purposes and will be removed in the future.
	IPAMProviderRandom IPAMProvider = "Random"
)

// IPAM represents the IP address management configuration for the created network interface.
type IPAM struct {
	// Provider represents the provider of IP address management for the created network interface.
	Provider IPAMProvider `json:"provider,omitempty"`

	// Random represents the configuration for the random IPAM provider.
	// This field can be set only when Provider is "random".
	// +optional
	Random *RandomIPAM `json:"random,omitempty"`
}

type RandomIPAM struct {
	// CIDR is the CIDR from which the random IP address will be assigned.
	CIDR string `json:"cidr,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DeviceNetworkList is a list of DeviceNetwork resources.
type DeviceNetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []DeviceNetwork `json:"items"`
}
