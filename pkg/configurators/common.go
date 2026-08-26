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
	"fmt"
	"math/rand/v2"
	"net"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	resourcev1 "k8s.io/api/resource/v1"
)

type Configurator interface {
	// IsSupported reports whether the given host device can be configured
	// according to the given DeviceConfiguration.
	IsSupported(
		ctx context.Context,
		hostDevice *host.Device,
		deviceConfiguration *v1alpha1.DeviceConfiguration,
	) (bool, error)

	// ExposedDevice configures the device which will be exposed in ResourceSlice.
	ExposedDevice(
		ctx context.Context,
		hostDevice *host.Device,
		device *resourcev1.Device,
	) (*resourcev1.Device, error)

	// Allocate allocates the network device by gathering the necessary information
	// and storing it in the ResourceClaim Device Status.
	//
	// The necessary information includes the interface name, parent device name, IPs
	// and other relevant information to configure the network device.
	Allocate(
		ctx context.Context,
		hostDevice *host.Device,
		deviceConfiguration *v1alpha1.DeviceConfiguration,
		networkInterfaceConfiguration *v1alpha1.NetworkInterfaceConfiguration,
		allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
	) (*resourcev1.AllocatedDeviceStatus, error)

	// Configure configures the device.
	//
	// It must be called when the pod is getting created and after the ResourceClaim is allocated.
	Configure(
		ctx context.Context,
		podNetworkNamespace string,
		allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
	) (*resourcev1.AllocatedDeviceStatus, error)

	// Release releases the device.
	//
	// It must be called when the Pod is getting deleted.
	Release(
		ctx context.Context,
		podNetworkNamespace string,
		allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
	) (*resourcev1.AllocatedDeviceStatus, error)
}

type CommonConfigurator struct {
}

func (c *CommonConfigurator) Allocate(
	ctx context.Context,
	hostDevice *host.Device,
	networkInterfaceConfiguration *v1alpha1.NetworkInterfaceConfiguration,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	if allocatedDeviceStatus == nil {
		return nil, fmt.Errorf("allocatedDeviceStatus is nil")
	}
	allocatedDeviceStatusRes := allocatedDeviceStatus.DeepCopy()

	if networkInterfaceConfiguration == nil {
		return nil, fmt.Errorf("networkInterfaceConfiguration is nil")
	}

	if allocatedDeviceStatus.NetworkData == nil {
		allocatedDeviceStatus.NetworkData = &resourcev1.NetworkDeviceData{}
	}

	if len(allocatedDeviceStatus.NetworkData.IPs) > 0 {
		return allocatedDeviceStatusRes, nil
	}

	for _, ipam := range networkInterfaceConfiguration.IPAM {
		if ipam.Provider == v1alpha1.IPAMProviderRandom {
			if ipam.Random == nil {
				return nil, fmt.Errorf("random IPAM configuration is nil")
			}
			if ipam.Random.CIDR == "" {
				return nil, fmt.Errorf("random IPAM CIDR is empty")
			}

			_, netIP, err := net.ParseCIDR(ipam.Random.CIDR)
			if err != nil {
				return nil, fmt.Errorf("invalid random IPAM CIDR: %v", err)
			}

			// generate a random IP address within the CIDR
			randomIP, err := generateRandomIP(netIP)
			if err != nil {
				return nil, fmt.Errorf("failed to generate random IP: %v", err)
			}

			ones, _ := netIP.Mask.Size()
			ip := fmt.Sprintf("%s/%d", randomIP.String(), ones)

			allocatedDeviceStatusRes.NetworkData.IPs = append(allocatedDeviceStatusRes.NetworkData.IPs, ip)
		}
	}

	return allocatedDeviceStatusRes, nil
}

func generateRandomIP(netIP *net.IPNet) (net.IP, error) {
	ones, bits := netIP.Mask.Size()
	if bits-ones < 2 {
		return nil, fmt.Errorf("subnet /%d has no usable host addresses", ones)
	}

	for {
		ip := make(net.IP, len(netIP.IP))
		copy(ip, netIP.IP)

		allZero := true
		allOne := true

		for i := range ip {
			hostBits := byte(rand.IntN(256)) & ^netIP.Mask[i]
			ip[i] |= hostBits

			if hostBits != 0 {
				allZero = false
			}

			if hostBits != ^netIP.Mask[i] {
				allOne = false
			}
		}

		if !allZero && !allOne {
			return ip, nil
		}
	}
}

// Release releases the device.
func (c *CommonConfigurator) Release(
	ctx context.Context,
	podNetworkNamespace string,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}
