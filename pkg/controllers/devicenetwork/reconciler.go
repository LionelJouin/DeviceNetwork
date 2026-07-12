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

package devicenetwork

import (
	"context"
	"fmt"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	deviceNetworkListers "github.com/lioneljouin/devicenetwork/pkg/client/listers/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/device"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
	schedulingcorev1 "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
)

// PublishResources is a function type to advertise resources.
type PublishResources func(context.Context, resourceslice.DriverResources) error

type DeviceConfigurator interface {
	ConfigureExposedDevice(ctx context.Context, deviceConfiguration v1alpha1.DeviceConfiguration, device *device.Device) *resourcev1.Device
}

type DeviceNetworkReconciler struct {
	nodeName string

	deviceCache *device.DeviceCache

	nodeLister corev1listers.NodeLister

	deviceNetworkLister deviceNetworkListers.DeviceNetworkLister

	publishResourcesFunc PublishResources

	deviceConfigurators map[v1alpha1.DeviceType]DeviceConfigurator
}

func NewDeviceNetworkReconciler(
	nodeName string,
	nodeLister corev1listers.NodeLister,
	deviceNetworkLister deviceNetworkListers.DeviceNetworkLister,
	publishResourcesFunc PublishResources,
	deviceCache *device.DeviceCache,
	deviceConfigurators map[v1alpha1.DeviceType]DeviceConfigurator,
) (*DeviceNetworkReconciler, error) {
	dnr := &DeviceNetworkReconciler{
		nodeName:             nodeName,
		nodeLister:           nodeLister,
		deviceNetworkLister:  deviceNetworkLister,
		publishResourcesFunc: publishResourcesFunc,
		deviceCache:          deviceCache,
		deviceConfigurators:  deviceConfigurators,
	}

	return dnr, nil
}

func (dnr *DeviceNetworkReconciler) Reconcile(ctx context.Context) error {
	node, err := dnr.nodeLister.Get(dnr.nodeName)
	if err != nil {
		return fmt.Errorf("failed to get node %s: %v", dnr.nodeName, err)
	}

	deviceNetworks, err := dnr.deviceNetworkLister.List(labels.ValidatedSetSelector{})
	if err != nil {
		return fmt.Errorf("failed to list DeviceNetworks: %v", err)
	}

	driverResources := dnr.getResources(ctx, deviceNetworks, node, dnr.deviceConfigurators)

	if dnr.publishResourcesFunc != nil {
		err = dnr.publishResourcesFunc(ctx, driverResources)
		if err != nil {
			return fmt.Errorf("failed to publish resources: %v", err)
		}
	}

	klog.FromContext(ctx).Info("Reconciled DeviceNetworks", "node", dnr.nodeName, "deviceNetworks", deviceNetworks, "resources", driverResources)

	return nil
}

func (dnr *DeviceNetworkReconciler) getResources(
	ctx context.Context,
	deviceNetworks []*v1alpha1.DeviceNetwork,
	currentNode *corev1.Node,
	deviceConfigurators map[v1alpha1.DeviceType]DeviceConfigurator,
) resourceslice.DriverResources {
	resourceDevices := []resourcev1.Device{}

	for _, deviceNetwork := range deviceNetworks {
		deviceForSelector := map[string][]*device.Device{}
		for _, deviceSelector := range deviceNetwork.Spec.DeviceSelectors {
			if deviceSelector.NodeSelector != nil {
				applyToNode, err := schedulingcorev1.MatchNodeSelectorTerms(currentNode, deviceSelector.NodeSelector)
				if err != nil || !applyToNode {
					continue
				}
			}

			devices := dnr.deviceCache.List(ctx, device.WithSelectors(deviceSelector.Selectors))
			deviceForSelector[deviceSelector.Name] = devices
		}

		for _, deviceConfiguration := range deviceNetwork.Spec.DeviceConfigurations {
			deviceType := v1alpha1.GetDeviceType(deviceConfiguration)
			configurator, ok := deviceConfigurators[deviceType]
			if !ok {
				continue
			}

			for _, selector := range deviceConfiguration.DeviceSelectors {
				devices, ok := deviceForSelector[selector]
				if !ok {
					continue
				}

				for _, dvc := range devices {
					// check if the device is already configured
					resourceDevice := configurator.ConfigureExposedDevice(ctx, deviceConfiguration, dvc)
					if resourceDevice == nil {
						continue
					}

					resourceDevice.Name = DeviceName(deviceNetwork.Name, deviceConfiguration.Name, dvc.Name)
					if resourceDevice.Attributes == nil {
						resourceDevice.Attributes = map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{}
					}

					resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesDeviceType)] = resourcev1.DeviceAttribute{StringValue: (*string)(&deviceType)}
					resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesPodNetwork)] = resourcev1.DeviceAttribute{StringValue: &deviceNetwork.Name}
					resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesNetworkKind)] = resourcev1.DeviceAttribute{StringValue: &deviceNetwork.Name}
					resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributesDeviceConfiguration)] = resourcev1.DeviceAttribute{StringValue: &deviceConfiguration.Name}

					resourceDevices = append(resourceDevices, *resourceDevice)
				}
			}
		}
	}

	driverResources := resourceslice.DriverResources{
		Pools: map[string]resourceslice.Pool{
			currentNode.Name: {Slices: []resourceslice.Slice{
				{
					Devices: resourceDevices,
				},
			}},
		},
	}

	return driverResources
}

func DeviceName(deviceNetworkName string, deviceConfigurationName string, deviceName string) string {
	return fmt.Sprintf("%s-%s-%s", deviceNetworkName, deviceConfigurationName, deviceName)
}
