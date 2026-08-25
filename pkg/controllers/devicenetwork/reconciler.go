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
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/host"
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

type DeviceNetworkReconciler struct {
	nodeName string

	networkKind string

	deviceCache *host.DeviceCache

	nodeLister corev1listers.NodeLister

	deviceNetworkLister deviceNetworkListers.DeviceNetworkLister

	publishResourcesFunc PublishResources

	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator
}

func NewDeviceNetworkReconciler(
	nodeName string,
	networkKind string,
	nodeLister corev1listers.NodeLister,
	deviceNetworkLister deviceNetworkListers.DeviceNetworkLister,
	publishResourcesFunc PublishResources,
	deviceCache *host.DeviceCache,
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator,
) (*DeviceNetworkReconciler, error) {
	dnr := &DeviceNetworkReconciler{
		nodeName:             nodeName,
		networkKind:          networkKind,
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
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator,
) resourceslice.DriverResources {
	resourceDevices := []resourcev1.Device{}

	for _, deviceNetwork := range deviceNetworks {
		deviceForSelector := map[string][]*host.Device{}
		for _, deviceSelector := range deviceNetwork.Spec.DeviceSelectors {
			if deviceSelector.NodeSelector != nil {
				applyToNode, err := schedulingcorev1.MatchNodeSelectorTerms(currentNode, deviceSelector.NodeSelector)
				if err != nil || !applyToNode {
					continue
				}
			}

			devices := dnr.deviceCache.List(ctx, host.WithSelectors(deviceSelector.Selectors))
			deviceForSelector[deviceSelector.Name] = devices
		}

		for _, deviceConfiguration := range deviceNetwork.Spec.DeviceConfigurations {
			deviceType := v1alpha1.GetDeviceType(deviceConfiguration)
			configurator, ok := deviceConfigurators[deviceType]
			if !ok {
				continue
			}

			deviceForDeviceConfiguration := map[string]*host.Device{} // key: device name, value: device
			for _, selector := range deviceConfiguration.DeviceSelectors {
				devices, ok := deviceForSelector[selector]
				if !ok {
					continue
				}
				for _, dvc := range devices {
					deviceForDeviceConfiguration[dvc.Name] = dvc
				}
			}

			for _, dvc := range deviceForDeviceConfiguration {
				// check if the device is already configured
				resourceDevice, err := configurator.ExposedDevice(ctx, dvc, nil)
				if err != nil || resourceDevice == nil {
					continue // todo
				}

				resourceDevice.Name = DeviceName(deviceNetwork.Name, deviceConfiguration.Name, dvc.Name)
				if resourceDevice.Attributes == nil {
					resourceDevice.Attributes = map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{}
				}

				resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceType)] = resourcev1.DeviceAttribute{StringValue: (*string)(&deviceType)}
				resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork)] = resourcev1.DeviceAttribute{StringValue: &deviceNetwork.Name}
				resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind)] = resourcev1.DeviceAttribute{StringValue: &dnr.networkKind}
				resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration)] = resourcev1.DeviceAttribute{StringValue: &deviceConfiguration.Name}
				resourceDevice.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName)] = resourcev1.DeviceAttribute{StringValue: &dvc.Name}

				resourceDevices = append(resourceDevices, *resourceDevice)
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
	// todo: DeviceName("a-b", "c", "d")  collides with DeviceName("a", "b-c", "d") and deviceName can contain unallowed characters,
	return fmt.Sprintf("%s-%s-%s", deviceNetworkName, deviceConfigurationName, deviceName)
}
