# Host Network Device

## Motivation

Specialized Kubernetes workloads are increasingly complex, requiring advanced networking capabilities. Common use cases include secondary networks, tenant isolation, SR-IOV, storage networks, and data-plane separation. These are critical for domains like AI/ML, HPC and Telco where predictable, reliable, high-performance networking is essential.

Today, host-backed networking is fragmented and inconsistent. Different technologies require separate mechanisms: SR-IOV uses a combination of a CNI plugin, a Device Plugin, and a separate SRIOVNetwork API; other host-backed network types like macvlan, ipvlan, or hostNetwork have no standardized integration with Kubernetes APIs or scheduling. This fragmentation leads to operational complexity and inconsistent management across workloads.

The introduction of `NetworkClass` provides an opportunity to bring clarity and consistency to host-backed networking. `NetworkClass` defines a model for representing pod networks as first-class resources within Kubernetes, and this design aligns with and builds upon that model. By leveraging `NetworkClass` and DRA, host network devices can be treated as Kubernetes-native resources, enabling predictable scheduling, allocation, and status reporting while maintaining a consistent interface and lifecycle management for workloads.

The API provides a uniform mechanism for device attachment, discovery, and status reporting across different host-backed networking technologies. By consolidating device management under a single API, it removes fragmentation, simplifies operational complexity, and ensures that network identity is fully represented and usable within Kubernetes, following the same abstraction principles as `NetworkClass`.

Initially, the API focuses on HostDevice (direct device assignment, e.g., SR-IOV VFs with full capabilities such as RDMA) and Macvlan. Future extensions may include Subfunction, IPVLAN, VLAN, and eventually bond, Team, MACVTAP, IPVTAP, or MACsec. Overlay or tunneling technologies are explicitly out of scope for this design.

**Goals:**
- Provide a unified and consistent Kubernetes-native abstraction for host-backed network devices (e.g., macvlan, IPVLAN, SR-IOV VF/SF, and direct device assignment...).
- Ensure that network identity of host-backed devices is fully exposed and usable by Kubernetes, compatible with `NetworkClass` design and DRA workflows.

**Non-Goals:**
- Provide a generic API that replaces or mandates CNI usage.
- Enable host network configuration, such as NIC setup, bridge creation, or low-level host networking changes.

## Proposal

This proposal builds on top of Dynamic Resource Allocation (DRA) and does not simply expose host network devices as they appear from the node’s perspective. Instead, it provides a mechanism to express and expose host-backed network devices in the form they are intended to be consumed by Pods. In other words, the abstraction focuses on how network devices should be configured and presented inside Pods, rather than mirroring the raw devices available on the host.

This design allows cluster operators to describe host network devices in terms of their intended usage and configuration, providing greater flexibility in how devices are exposed and consumed. It enables administrators to control which devices are available, how they are derived (for example via macvlan), and how the resulting interfaces are configured inside Pods.

To achieve this, the proposal introduces a new API called `HostNetworkDevice`. This API models host-backed networking through three sequential steps.

The first step is host network device selection. In this step, host network devices are selected based on node placement and device characteristics. A NodeSelector allows limiting the selection to a subset of nodes in the cluster, while a CEL expression can be used to match specific device attributes. For example, devices may be selected based on their interface name, link type, PCI attributes, or other characteristics. This step allows grouping devices from different nodes that share common properties and should be treated similarly.

The second step is host network device configuration. This step defines how the previously selected devices should be exposed to Pods. The configuration determines the type of device that will ultimately be attached to the Pod. For example, the device may be exposed as a HostDevice, where the host interface is moved directly into the Pod network namespace, or as a Macvlan, where a new macvlan interface is created on top of the selected host interface before being attached to the Pod. Depending on the chosen type, additional configuration options may be provided. For instance, macvlan devices may specify a mode such as private, bridge, or vepa. At this stage, the resulting device representations are advertised through ResourceSlice objects using the DRA framework.

The third and final step is network interface configuration. Once a device has been allocated to a Pod and created according to the selected configuration, a common interface configuration is applied. This configuration defines runtime networking properties such as IP addresses, routes, and other interface-level parameters that should be applied to the interface once it is attached to the Pod.

Together, these three steps allow cluster administrators to define host-backed networking resources declaratively, while ensuring that device discovery, allocation, and lifecycle management follow the standard Kubernetes workflow.

### API

`HostNetworkDevice` is a cluster-scoped resource structured around the three steps described in the proposal: device selection, device configuration, and network interface configuration.

```golang
// HostNetworkDevice is a specification for a HostNetworkDevice resource.
type HostNetworkDevice struct {
	metav1.TypeMeta `json:",inline"`
	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired behavior of the HostNetworkDevice.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	// +optional
	Spec HostNetworkDeviceSpec `json:"spec"`

	// Most recently observed status of the HostNetworkDevice.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	// +optional
	Status HostNetworkDeviceStatus `json:"status"`
}

const (
	DeviceSelectorMaxSize      = 4
	DeviceConfigurationMaxSize = 4
)

// HostNetworkDeviceSpec is the spec for a HostNetworkDevice resource.
// HostNetworkDevice represents a set of host network devices which are
// selected by the DeviceSelector and configured by the DeviceConfiguration.
//
// The HostNetworkDevice consists of three main components (DeviceSelectors,
// DeviceConfiguration and NetworkInterfaceConfiguration) chained together
// to represent a set of network interfaces.
//
// Example:
// 1. A host network device with the interface name "eno1np0" is selected by a
// DeviceSelector on "node-a".
//
// 2. The selected device "eno1np0" will be exposed as a new device in a
// ResourceSlice on the node "node-a" with its "name + <DeviceConfiguration.Name>"
// and the related attributes.
//
// 3. When the device is claimed by a Pod, the device will be configured according to the
// DeviceConfiguration and the NetworkInterfaceConfiguration. For example, if the
// DeviceConfiguration specifies a Macvlan configuration, then a Macvlan interface will
// be created on top of "eno1np0" and the created Macvlan interface will be configured
// in the Pod.
type HostNetworkDeviceSpec struct {
	// DeviceSelectors selects host network devices on
	// specific nodes based on the specified selection criteria.
	//
	// The maximum number of DeviceSelectors is 4. This limit could be increased
	// in the future based on the actual needs.
	//
	// +required
	DeviceSelectors []DeviceSelector `json:"deviceSelectors,omitempty"`

	// DeviceConfigurations defines the configuration for
	// host network devices selected by the DeviceSelector.
	//
	// The maximum number of DeviceConfigurations is 4. This limit could be increased
	// in the future based on the actual needs.
	//
	// +required
	DeviceConfigurations []DeviceConfiguration `json:"deviceConfigurations,omitempty"`

	// NetworkInterfaceConfiguration defines the configuration for the network interface
	// created based on the selected host network device and the DeviceConfiguration.
	// The configuration will be applied to all the network interfaces created based
	// on the selected host network device and the DeviceConfiguration.
	//
	// +optional
	NetworkInterfaceConfiguration NetworkInterfaceConfiguration `json:"networkInterfaceConfiguration,omitempty"`
}

// HostNetworkDeviceStatus is the status for a HostNetworkDevice resource.
type HostNetworkDeviceStatus struct {
}

// DeviceSelector represents the selection criteria for selecting host network
// devices on specific nodes.
// A DeviceSelector must be referenced by at least one DeviceConfiguration and can
// be referenced by multiple DeviceConfigurations.
type DeviceSelector struct {
	// Name is the identifier for this selection. The name is used to reference
	// this selection from the DeviceConfigurations.
	//
	// +required
	Name string `json:"name,omitempty"`

	// Selectors defines the selection criteria for selecting host network devices.
	// Each selector must be satisfied for a host network device to be selected by
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

// CELDeviceSelector contains a CEL expression for selecting a host network device.
type CELDeviceSelector struct {
	// Expression is a CEL expression which evaluates a single host network device.
	// It must evaluate to true when the device under consideration satisfies
	// the desired criteria, and false when it does not.
	//
	// The expression's input is an object named "device", which carries
	// the following properties:
	//  - attributes (map[string]object): the device's attributes.
	//  - capacity (map[string]object): the device's capacities.
	//
	// Example: Consider a set of host network devices which are rdma capable and
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

// DeviceConfiguration defines the configuration for host network
// devices selected by the DeviceSelector.
//
// Based on the DeviceType, the network devices selected by the DeviceSelector
// which do not support the specified DeviceType will be filtered out and will
// not be considered.
// For example, if the DeviceType is "vlan", then the selected host network devices
// which are already configured as "vlan" interface will not be considered as Linux
// does not allow creating a vlan interface on top of a device which is already configured
// as vlan.
type DeviceConfiguration struct {
	// Name is an identifier for this configuration.
	// It can be used to reference this configuration from other resources.
	//
	// +required
	Name string `json:"name,omitempty"`

	// DeviceSelectors defines the selections for host network
	// devices to which this configuration will be applied.
	// All the selected network devices will be configured according to this configuration.
	// This field is required and must reference at least one DeviceSelector.
	// A DeviceSelector cannot be referenced multiple times by the same DeviceConfiguration.
	//
	// +required
	DeviceSelectors []string `json:"deviceSelectors,omitempty"`

	// DeviceType defines how the selected host network device
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
	// DeviceTypeHostDevice indicates that the selected host
	// network device will be moved to the pod's network namespace
	// without any additional configuration.
	DeviceTypeHostDevice DeviceType = "HostDevice"

	// DeviceTypeMacvlan indicates that a Macvlan interface will be
	//  created on top of the selected host network device.
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
	// todo
}
```

### Attributes

Each device advertised in a ResourceSlice carries a set of standardized DRA attributes. These attributes serve two purposes: they enable CEL-based selection in ResourceClaims and DeviceClasses, and they provide visibility into the properties of the underlying host device. The attributes are grouped into device metadata (device type, pod network, network class), host device properties (name, link layer type, driver, MAC, MTU, VLAN ID, RDMA capability), and hardware topology (PCI address, PCI root, NUMA node).

```golang
// NetworkInterfaceAttribute represents the attributes of a network interface.
type NetworkInterfaceAttribute string

const (
	// NetworkInterfaceAttributesPrefix is the prefix used for network interface attributes.
	NetworkInterfaceAttributesPrefix = "network.device.hostnetworkdevice.io"

	// NetworkInterfaceAttributesDeviceType represents the type of the network interface to be configured.
	// This is determined by the deviceType field in the DeviceConfiguration.
	// e.g. HostDevice, Macvlan...
	// The value type of this attribute is string.
	NetworkInterfaceAttributesDeviceType NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "deviceType"
	// NetworkInterfaceAttributesPodNetwork represents the name of the HostNetworkDevice object used to configure this
	// device.
	// The value type of this attribute is string.
	NetworkInterfaceAttributesPodNetwork NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "podNetwork"
	// NetworkInterfaceAttributesNetworkClass represents the type of the object used to configure this device.
	// The value will always be "HostNetworkDevice".
	// The value type of this attribute is string.
	NetworkInterfaceAttributesNetworkClass NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "networkClass"

	// Attributes with the prefix "hostDevice" represent the attributes of the host
	// device used to create this network interface.

	// NetworkInterfaceAttributesHostDeviceName represents the name of the host device used to create this network interface.
	// The value type of this attribute is string.
	NetworkInterfaceAttributesHostDeviceName NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceName"
	// NetworkInterfaceAttributesHostDeviceLinkLayerType represents the link layer type of the host device.
	// e.g., Ethernet, Infiniband...
	// The value type of this attribute is string.
	NetworkInterfaceAttributesHostDeviceLinkLayerType NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceLinkLayerType"
	// NetworkInterfaceAttributesHostDeviceType represents the type of the host device.
	// e.g., Macvlan, Ipvlan...
	// The value type of this attribute is string.
	NetworkInterfaceAttributesHostDeviceType NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceType"
	// NetworkInterfaceAttributesHostDeviceIPs represents the IP addresses of the host device.
	// The value type of this attribute is string.
	NetworkInterfaceAttributesHostDeviceIPs NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceIPs"
	// NetworkInterfaceAttributesHostDeviceVendor represents the vendor of the host device.
	// The value type of this attribute is string.
	NetworkInterfaceAttributesHostDeviceVendor NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceVendor"
	// NetworkInterfaceAttributesHostDeviceDriver represents the driver of the host device.
	// The value type of this attribute is string.
	NetworkInterfaceAttributesHostDeviceDriver NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceDriver"
	// NetworkInterfaceAttributesHostDeviceVlanId represents the VLAN ID of the host device.
	// The value type of this attribute is integer between 0 and 4095.
	NetworkInterfaceAttributesHostDeviceVlanId NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceVlanId"
	// NetworkInterfaceAttributesHostDeviceMTU represents the Maximum Transmission Unit of the host device.
	// The value type of this attribute is integer.
	NetworkInterfaceAttributesHostDeviceMTU NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceMTU"
	// NetworkInterfaceAttributesHostDeviceMac represents the MAC address of the host device.
	// The value type of this attribute is string.
	NetworkInterfaceAttributesHostDeviceMac NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceMac"
	// NetworkInterfaceAttributesHostDeviceRDMACapable represents whether the host device is RDMA capable.
	// The value type of this attribute is boolean.
	NetworkInterfaceAttributesHostDeviceRDMACapable NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "hostDeviceRDMACapable"

	// NetworkInterfaceAttributesPCIRoot represents the PCI root of the host device.
	// The value type of this attribute is string.
	NetworkInterfaceAttributesPCIRoot NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "pciRoot"
	// NetworkInterfaceAttributesPCIAddress represents the PCI address of the host device.
	// The value type of this attribute is string.
	NetworkInterfaceAttributesPCIAddress NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "pciAddress"
	// NetworkInterfaceAttributesNUMA represents the NUMA node of the host device.
	// The value type of this attribute is integer.
	NetworkInterfaceAttributesNUMA NetworkInterfaceAttribute = NetworkInterfaceAttributesPrefix + "/" + "numa"
)
```

### Example

#### Basic - All host network devices

This example selects all host network devices across the cluster and exposes them as HostDevice, meaning each host interface is moved directly into the Pod network namespace when claimed.

HostNetworkDevice:
```yaml
apiVersion: multinetwork.networking.k8s.io/v1alpha1
kind: HostNetworkDevice
metadata:
  name: all-interfaces-network
spec:
  deviceSelectors:
  - name: "all-interfaces"
  deviceConfigurations: 
  - name: "HostDevice-all-interfaces"
    deviceType: HostDevice
    deviceSelectors:
    - "all-interfaces"
```

On a single-node cluster with two network interfaces (eth0 and eth1), the resulting ResourceSlice advertises one device per interface, each carrying its host device attributes and PCI topology:
```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  name: all-interfaces-network-worker-a
spec:
  nodeName: worker-a
  devices:
  - name: all-interfaces-network-hostdevice-all-interfaces-eth0
    attributes:
      network.device.hostnetworkdevice.io/deviceType:
          string: HostDevice
      network.device.hostnetworkdevice.io/podNetwork:
          string: all-interfaces-network
      network.device.hostnetworkdevice.io/hostDeviceName:
          string: eth0
      network.device.hostnetworkdevice.io/hostDeviceLinkLayerType:
          string: ethernet
      network.device.hostnetworkdevice.io/pciRoot:
          string: 0000:c4
      network.device.hostnetworkdevice.io/pciAddress:
          string: 0000:c4:00.0
      network.device.hostnetworkdevice.io/numa:
          integer: 0
  - name: all-interfaces-network-hostdevice-all-interfaces-eth1
    attributes:
      network.device.hostnetworkdevice.io/deviceType:
          string: HostDevice
      network.device.hostnetworkdevice.io/podNetwork:
          string: all-interfaces-network
      network.device.hostnetworkdevice.io/hostDeviceName:
          string: eth1
      network.device.hostnetworkdevice.io/hostDeviceLinkLayerType:
          string: ethernet
      network.device.hostnetworkdevice.io/pciRoot:
          string: 0000:c4
      network.device.hostnetworkdevice.io/pciAddress:
          string: 0000:c4:00.1
      network.device.hostnetworkdevice.io/numa:
          integer: 0
```

#### Overlapping device

This example illustrates how multiple `HostNetworkDevice` objects and multiple `DeviceConfigurations` can reference the same underlying host interfaces. The "blue-network" exposes all interfaces both as HostDevice and as Macvlan, while the "red-network" exposes them as Macvlan only. Since these configurations overlap on the same physical devices, the ResourceSlice uses shared counters to enforce mutual exclusion: a HostDevice allocation takes full ownership of the interface, preventing any Macvlan allocation on the same device, while Macvlan allocations coexist with each other.

HostNetworkDevice:
```yaml
apiVersion: multinetwork.networking.k8s.io/v1alpha1
kind: HostNetworkDevice
metadata:
  name: blue-network
spec:
  deviceSelectors:
  - name: "all-interfaces"
  deviceConfigurations: 
  - name: "HostDevice-all-interfaces"
    deviceType: HostDevice
    deviceSelectors:
    - "all-interfaces"
  - name: "Macvlan-all-interfaces"
    deviceType: Macvlan
    deviceSelectors:
    - "all-interfaces"
---
apiVersion: multinetwork.networking.k8s.io/v1alpha1
kind: HostNetworkDevice
metadata:
  name: red-network
spec:
  deviceSelectors:
  - name: "all-interfaces"
  deviceConfigurations: 
  - name: "HostDevice-all-interfaces"
    deviceType: Macvlan
    deviceSelectors:
    - "all-interfaces"
```

On a single-node cluster with one network interface (eth0), the resulting ResourceSlice uses shared counters to coordinate access across the three device representations of the same physical interface:

```yaml
---
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  name: all-interfaces-network-worker-a
spec:
  nodeName: worker-a
  sharedCounters:
  - name: eth0
    counters:
      mutual-exclusion: # To avoid a host-device to take the interface while host devices use it and vise-versa.
        value: 65536
  devices:
  - name: blue-network-hostdevice-all-interfaces-eth0
    attributes:
      network.device.hostnetworkdevice.io/deviceType:
          string: HostDevice
      network.device.hostnetworkdevice.io/podNetwork:
          string: all-interfaces-network
      network.device.hostnetworkdevice.io/hostDeviceName:
          string: eth0
      network.device.hostnetworkdevice.io/hostDeviceLinkLayerType:
          string: ethernet
      network.device.hostnetworkdevice.io/pciRoot:
          string: 0000:c4
      network.device.hostnetworkdevice.io/pciAddress:
          string: 0000:c4:00.0
      network.device.hostnetworkdevice.io/numa:
          integer: 0
    consumesCounters:
    - counterSet: eth0
      counters:
        mutual-exclusion:
          value: 65536 # Takes the full ownership
  - name: blue-interfaces-network-macvlan-all-interfaces-eth0
    allowMultipleAllocations: true
    attributes:
      network.device.hostnetworkdevice.io/deviceType:
          string: Macvlan
      network.device.hostnetworkdevice.io/podNetwork:
          string: all-interfaces-network
      network.device.hostnetworkdevice.io/hostDeviceName:
          string: eth0
      network.device.hostnetworkdevice.io/hostDeviceLinkLayerType:
          string: ethernet
      network.device.hostnetworkdevice.io/pciRoot:
          string: 0000:c4
      network.device.hostnetworkdevice.io/pciAddress:
          string: 0000:c4:00.0
      network.device.hostnetworkdevice.io/numa:
          integer: 0
    network.device.hostnetworkdevice.io/virtualDeviceUnlimited: # "Unlimited" capacity as virtual interfaces will be created
      requestPolicy:
        default: 1
        validValues:
        - 1
      value: 65536
    consumesCounters:
    - counterSet: eth0
      counters:
        mutual-exclusion:
          value: 1
  - name: red-interfaces-network-macvlan-all-interfaces-eth0
    allowMultipleAllocations: true
    attributes:
      network.device.hostnetworkdevice.io/deviceType:
          string: Macvlan
      network.device.hostnetworkdevice.io/podNetwork:
          string: all-interfaces-network
      network.device.hostnetworkdevice.io/hostDeviceName:
          string: eth0
      network.device.hostnetworkdevice.io/hostDeviceLinkLayerType:
          string: ethernet
      network.device.hostnetworkdevice.io/pciRoot:
          string: 0000:c4
      network.device.hostnetworkdevice.io/pciAddress:
          string: 0000:c4:00.0
      network.device.hostnetworkdevice.io/numa:
          integer: 0
    network.device.hostnetworkdevice.io/virtualDeviceUnlimited: # "Unlimited" capacity as virtual interfaces will be created
      requestPolicy:
        default: 1
        validValues:
        - 1
      value: 65536
    consumesCounters:
    - counterSet: eth0
      counters:
        mutual-exclusion:
          value: 1
```

### Implementation

The `HostNetworkDevice` implementation runs as a node agent deployed on each node in the cluster. The node agent serves two roles: it acts as a DRA driver responsible for device discovery and ResourceSlice management, and it acts as an NRI plugin responsible for configuring network interfaces at container runtime. In addition, the node agent acts as a controller that watches and reconciles both the `HostNetworkDevice` objects in the cluster and the host network interfaces present on the node.

TODO: How device selection works

#### DRA Driver

The DRA driver component is responsible for discovering host network interfaces and advertising them as devices through the DRA ResourceSlice API. On startup, the node agent enumerates the network interfaces available on the host and retrieves the set of `HostNetworkDevice` objects from the API server. For each `HostNetworkDevice`, the agent evaluates its device selectors against the discovered host interfaces. When a host interface matches a selector, the agent applies the associated device configurations to determine how the device should be represented. The resulting devices are published into ResourceSlices, each carrying the standardized attributes defined by the `HostNetworkDevice` specification.

The node agent continuously monitors both the host network interfaces and the `HostNetworkDevice` objects for changes. When a new interface appears on the host, an existing interface is removed, or a `HostNetworkDevice` object is created or updated, the agent re-evaluates the selectors and updates the ResourceSlices accordingly. This ensures that the advertised devices always reflect the current state of the node.

TODO: what attributes are exposed

#### NRI Plugin

The NRI plugin component is responsible for configuring and tearing down network interfaces inside Pod network namespaces in response to container lifecycle events.

When the container runtime triggers a `RunPodSandbox` or `CreateContainer` event, the NRI plugin identifies which devices have been allocated to the Pod through the DRA allocation. It retrieves the corresponding `HostNetworkDevice` object using the `network.device.hostnetworkdevice.io/podNetwork` attribute of the allocated device. Based on the `DeviceType` specified in the matching device configuration, the plugin performs the appropriate network setup. For a HostDevice type, the host interface is moved directly into the Pod network namespace. For a Macvlan type, a new macvlan interface is created on top of the host interface using the configured mode, and the newly created interface is moved into the Pod network namespace. Once the interface is in place, the `NetworkInterfaceConfiguration` is applied to set runtime properties such as IP addresses and routes.

When the container runtime triggers a `RemovePodSandbox` or `RemoveContainer` event, the NRI plugin reverses the setup. For a HostDevice type, the interface is moved back to the host network namespace. For a Macvlan type, the macvlan interface is deleted. In both cases, the host device is restored to its original state so that it can be reused by future allocations.

#### Lifecycle

A `HostNetworkDevice` object is immutable once created. Its device selectors, device configurations, and network interface configuration cannot be modified after creation. To change the configuration, the existing object must be deleted and a new one created.

A `HostNetworkDevice` object cannot be deleted while at least one Pod is still attached to a device belonging to it. This ensures that the specification needed to properly manage and clean up network interfaces remains available for the duration of the Pod lifecycle.
