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
	resourcev1 "k8s.io/api/resource/v1"
)

type Common struct {
}

func (c *Common) Allocate(
	ctx context.Context,
) (*v1alpha1.ResourceClaimDeviceStatusData, error) {
	networkDeviceData := &resourcev1.NetworkDeviceData{
		InterfaceName: randomName(),
	}

	fmt.Println(networkDeviceData)
	return nil, nil
}

// func (mcvln *Common) Configure(
// 	ctx context.Context,
// 	podNetworkNamespace string,
// 	deviceConfiguration *v1alpha1.DeviceConfiguration,
// 	device *resourcev1.Device,
// ) error {
// 	return nil
// }

// func (mcvln *Common) ConfigureExposedDevice(
// 	ctx context.Context,
// 	deviceConfiguration v1alpha1.DeviceConfiguration,
// 	device *device.Device,
// ) *resourcev1.Device {
// 	return &resourcev1.Device{
// 		Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
// 	}
// }
