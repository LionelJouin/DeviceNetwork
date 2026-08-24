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
	"k8s.io/utils/ptr"
)

func GetDeviceType(deviceConfiguration DeviceConfiguration) DeviceType {
	if deviceConfiguration.DeviceType == nil {
		return DeviceTypeHostDevice
	}

	return *deviceConfiguration.DeviceType
}

func GetMacvlan(deviceConfiguration DeviceConfiguration) *Macvlan {
	// default values for macvlan configuration
	macvlan := &Macvlan{
		Mode: ptr.To(MacvlanModeBridge),
	}

	if deviceConfiguration.Macvlan != nil {
		if deviceConfiguration.Macvlan.Mode != nil {
			macvlan.Mode = deviceConfiguration.Macvlan.Mode
		}
	}

	return macvlan
}
