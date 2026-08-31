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

type HostDevice struct {
	// sysfsNetRoot is the sysfs path holding the network device class, used to
	// discover the RDMA devices backing an interface. It defaults to
	// host.DefaultSysfsNetRoot ("/sys/class/net") in production; tests override
	// it with a temporary directory to mock RDMA discovery.
	sysfsNetRoot string
}

// IsSupported reports whether the given host device can be configured
// according to the given DeviceConfiguration.
//
// A device is supported only if it can be safely moved into the pod network
// namespace and returned to the host when the pod is deleted. The following
// are rejected:
//   - loopback: every namespace has its own lo; the kernel refuses to move it
//   - bridge, bond, team: carry NETIF_F_NETNS_LOCAL; the kernel refuses to move them
//   - tun/tap (type "tuntap"): fd-based; severing the fd breaks the device and it may be destroyed
//   - ParentIndex != 0 (macvlan, vlan, ipvlan, macsec, …): destroyed on netns deletion
//   - MasterIndex != 0: enslaved to a bridge or bond; must be freed before moving
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
	// Tun/tap devices are fd-based; moving them severs the fd-holder's access
	// and the device may be destroyed when all fds are closed.
	// Note: vishvananda/netlink maps IFLA_INFO_KIND "tun" to Type() "tuntap".
	switch link.Type() {
	case "bridge", "bond", "team", "tuntap":
		return false, nil
	}

	// Virtual devices with a parent interface (macvlan, vlan, ipvlan, macsec,
	// etc.) are destroyed rather than returned to the host when the pod
	// namespace is deleted, so they cannot be safely used as host devices.
	if link.Attrs().ParentIndex != 0 {
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
	if allocatedDeviceStatus == nil {
		return nil, fmt.Errorf("allocatedDeviceStatus is nil")
	}
	allocatedDeviceStatusRes := allocatedDeviceStatus.DeepCopy()

	if allocatedDeviceStatus.Data == nil || allocatedDeviceStatus.Data.Raw == nil {
		return nil, fmt.Errorf("allocated device status data is nil")
	}

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

	if v1alpha1.GetDeviceType(*resourceClaimDeviceStatusData.DeviceConfiguration) != v1alpha1.DeviceTypeHostDevice {
		return nil, fmt.Errorf("allocated device status data does not contain host device configuration information")
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

	ifName := resourceClaimDeviceStatusData.Device.Spec.InterfaceName

	// Resolve the RDMA devices backing the interface before moving anything:
	// discovery reads <sysfsNetRoot>/<ifName>/device/infiniband, which no longer
	// resolves on the host once the netdev has left the host network namespace.
	sysfsNetRoot := hd.sysfsNetRoot
	if sysfsNetRoot == "" {
		sysfsNetRoot = host.DefaultSysfsNetRoot
	}

	rdmaDevices, err := host.RDMADevicesForNetdev(sysfsNetRoot, ifName)
	if err != nil {
		return nil, err
	}

	// Move the RDMA device(s) into the pod network namespace.
	for _, rdmaDevName := range rdmaDevices {
		rdmaLink, err := netlink.RdmaLinkByName(rdmaDevName)
		if err != nil {
			return nil, fmt.Errorf("failed to get RDMA link %q: %v", rdmaDevName, err)
		}
		if err := netlink.RdmaLinkSetNsFd(rdmaLink, uint32(nsHandle)); err != nil {
			return nil, fmt.Errorf("failed to move RDMA link %q to pod network namespace: %v", rdmaDevName, err)
		}
	}

	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return nil, fmt.Errorf("failed to get link %q: %v", ifName, err)
	}

	if err := netlink.LinkSetNsFd(link, int(nsHandle)); err != nil {
		return nil, fmt.Errorf("failed to move link %q to pod network namespace: %v", ifName, err)
	}

	return allocatedDeviceStatusRes, nil
}

// Release releases the device.
//
// It must be called when the Pod is getting deleted.
//
// No explicit network cleanup is required: when the pod network namespace is
// deleted, the kernel automatically moves physical interfaces (and associated
// RDMA devices) still inside it back to the host network namespace. Virtual
// devices with a parent in another namespace are excluded from HostDevice by
// IsSupported, so they are never moved here.
func (hd *HostDevice) Release(
	ctx context.Context,
	podNetworkNamespace string,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}
