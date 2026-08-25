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
package status

import (
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/host"
)

// ResourceClaimDeviceStatusData represents the status data for a device in a ResourceClaim.
type ResourceClaimDeviceStatusData struct {
	// DeviceNetwork is the name of the DeviceNetwork
	// which was used to configure the device.
	DeviceNetwork string `json:"deviceNetwork,omitempty"`

	// DeviceConfiguration is the configuration for the device.
	DeviceConfiguration *v1alpha1.DeviceConfiguration `json:"deviceConfiguration,omitempty"`

	// Device is the host network device for which the device configuration is being applied.
	Device *host.Device `json:"device,omitempty"`
}

// Well-known condition types for ResourceClaim Device Status.
const (
	// DeviceStatusConditionAllocation in the DeviceStatus condition indicates
	// whether the device has been allocated to the ResourceClaim.
	DeviceStatusConditionAllocation = "Allocation"
)

// Well-known condition reasons for ResourceClaim Device Status.
const (
	// DeviceStatusReasonAllocated in the Allocation condition indicates
	// that the device has been allocated to the ResourceClaim and is ready to
	// be configured in the Pod.
	DeviceStatusReasonAllocated = "Allocated"
	// DeviceStatusReasonError in the Allocation condition indicates
	// that there was an error allocating the device to the ResourceClaim.
	DeviceStatusReasonError = "Error"
	// DeviceStatusReasonDeallocated in the Allocation condition indicates
	// that the device has been deallocated from the ResourceClaim and is no longer
	// available for use in the Pod.
	DeviceStatusReasonDeallocated = "Deallocated"
)
