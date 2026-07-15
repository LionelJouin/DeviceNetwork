/*
Copyright 2026

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

package resolver

import (
	"context"
	"fmt"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	v1alpha1devicenetworkinformers "github.com/lioneljouin/devicenetwork/pkg/client/informers/externalversions/apis/v1alpha1"
	deviceNetworkListers "github.com/lioneljouin/devicenetwork/pkg/client/listers/apis/v1alpha1"
	resourcev1 "k8s.io/api/resource/v1"
	resourceinformers "k8s.io/client-go/informers/resource/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/dynamic-resource-allocation/structured"
)

const (
	// deviceIDIndex is the lookup name for the index function
	// which indexes devices by deviceID.
	deviceIDIndex = "deviceId"
)

type Resolver struct {
	networkKind string

	resourceSliceIndexer cache.Indexer

	deviceNetworkLister deviceNetworkListers.DeviceNetworkLister

	resourceSliceSynced cache.InformerSynced
	deviceNetworkSynced cache.InformerSynced
}

type Device struct {
	DeviceRequestAllocationResult *resourcev1.DeviceRequestAllocationResult
	DeviceNetwork                 *v1alpha1.DeviceNetwork
	DeviceConfiguration           *v1alpha1.DeviceConfiguration
	ExposedDevice                 *resourcev1.Device
}

func NewResolver(
	networkKind string,
	resourceSliceInformer resourceinformers.ResourceSliceInformer,
	deviceNetworkInformer v1alpha1devicenetworkinformers.DeviceNetworkInformer,
) (*Resolver, error) {
	r := &Resolver{
		networkKind:          networkKind,
		resourceSliceIndexer: resourceSliceInformer.Informer().GetIndexer(),
		deviceNetworkLister:  deviceNetworkInformer.Lister(),
		resourceSliceSynced:  resourceSliceInformer.Informer().HasSynced,
		deviceNetworkSynced:  deviceNetworkInformer.Informer().HasSynced,
	}

	err := r.resourceSliceIndexer.AddIndexers(cache.Indexers{
		deviceIDIndex: func(obj interface{}) ([]string, error) {
			rs, ok := obj.(*resourcev1.ResourceSlice)
			if !ok {
				return nil, nil
			}

			res := []string{}

			for _, device := range rs.Spec.Devices {
				key := structured.MakeDeviceID(rs.Spec.Driver, rs.Spec.Pool.Name, device.Name).String()
				res = append(res, key)
			}

			return res, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add indexer for NetworkKind: %w", err)
	}

	return r, nil
}

func (r *Resolver) Run(ctx context.Context, workers int) error {
	if !cache.WaitForNamedCacheSyncWithContext(
		ctx,
		r.resourceSliceSynced,
		r.deviceNetworkSynced,
	) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	<-ctx.Done()

	return nil
}

func (r *Resolver) GetDevices(
	driverName string,
	claim *resourcev1.ResourceClaim,
) ([]*Device, error) {
	var devices []*Device

	for _, claimedDevice := range claim.Status.Allocation.Devices.Results {
		if claimedDevice.Driver != driverName {
			continue
		}

		deviceNetwork, deviceConfiguration, exposedDevice, err := r.getDeviceNetworkForDevice(&claimedDevice)
		if err != nil {
			return nil, fmt.Errorf("failed to get device network for device %s.%s.%s: %v", claimedDevice.Driver, claimedDevice.Pool, claimedDevice.Device, err)
		}

		devices = append(devices, &Device{
			DeviceRequestAllocationResult: &claimedDevice,
			DeviceNetwork:                 deviceNetwork,
			DeviceConfiguration:           deviceConfiguration,
			ExposedDevice:                 exposedDevice,
		})
	}
	return devices, nil
}

func (r *Resolver) getDeviceNetworkForDevice(
	deviceRequestAllocationResult *resourcev1.DeviceRequestAllocationResult,
) (*v1alpha1.DeviceNetwork, *v1alpha1.DeviceConfiguration, *resourcev1.Device, error) {
	key := structured.MakeDeviceID(deviceRequestAllocationResult.Driver, deviceRequestAllocationResult.Pool, deviceRequestAllocationResult.Device).String()

	objs, err := r.resourceSliceIndexer.ByIndex(deviceIDIndex, key)
	if err != nil || len(objs) == 0 {
		return nil, nil, nil, fmt.Errorf("no device found for deviceID %s: %v", key, err)
	}

	slice, ok := objs[0].(*resourcev1.ResourceSlice)
	if !ok {
		return nil, nil, nil, fmt.Errorf("unexpected type for deviceID %s: %T", key, objs[0])
	}

	var device *resourcev1.Device
	for i, dev := range slice.Spec.Devices {
		if dev.Name == deviceRequestAllocationResult.Device {
			device = &slice.Spec.Devices[i]
			break
		}
	}

	podNetwork, ok := device.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesPodNetwork)]
	if !ok || podNetwork.StringValue == nil {
		return nil, nil, nil, fmt.Errorf("device %s does not have pod network attribute", device.Name)
	}
	networkKind, ok := device.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesNetworkKind)]
	if !ok || networkKind.StringValue == nil || *networkKind.StringValue != r.networkKind {
		return nil, nil, nil, fmt.Errorf("device %s does not have the expected network kind attribute", device.Name)
	}
	deviceConfigurationName, ok := device.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesDeviceConfiguration)]
	if !ok || deviceConfigurationName.StringValue == nil {
		return nil, nil, nil, fmt.Errorf("device %s does not have device configuration attribute", device.Name)
	}

	deviceNetwork, err := r.deviceNetworkLister.Get(*podNetwork.StringValue)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get device network %s: %v", *podNetwork.StringValue, err)
	}

	var deviceConfiguration *v1alpha1.DeviceConfiguration
	for _, dc := range deviceNetwork.Spec.DeviceConfigurations {
		if dc.Name == *deviceConfigurationName.StringValue {
			deviceConfiguration = &dc
			break
		}
	}
	if deviceConfiguration == nil {
		return nil, nil, nil, fmt.Errorf("device configuration %s not found in device network %s", *deviceConfigurationName.StringValue, deviceNetwork.Name)
	}

	return deviceNetwork, deviceConfiguration, device, nil
}
