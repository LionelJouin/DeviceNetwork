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

// Package driver implements a DRA Kubelet plugin
// storing ResourceClaims of pods to later call CNI plugins
// on Pod creation.
package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	multinetworkv1alpha1 "github.com/kubernetes-sigs/multi-network-api/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/resolver"
	"github.com/lioneljouin/devicenetwork/pkg/status"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	resourceapply "k8s.io/client-go/applyconfigurations/resource/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
)

var _ kubeletplugin.DRAPlugin = &Driver{}

// PodResourceStore is an interface to store ResourceClaims of pods.
type PodResourceStore interface {
	Add(podUID types.UID, allocation *resourcev1.ResourceClaim)
}

// deviceResolver is an interface to resolve devices for a given ResourceClaim.
type deviceResolver interface {
	GetDevice(deviceRequestAllocationResult *resourcev1.DeviceRequestAllocationResult) (*resolver.Device, error)
}

// Driver represents a DRA Kubelet plugin.
type Driver struct {
	podNetworkKind      string
	driverName          string
	kubeClient          kubernetes.Interface
	draPlugin           *kubeletplugin.Helper
	podResourceStore    PodResourceStore
	deviceResolver      deviceResolver
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator
}

// Start the DRA Kubelet plugin.
func Start(
	ctx context.Context,
	podNetworkKind string,
	driverName string,
	nodeName string,
	kubeClient kubernetes.Interface,
	podResourceStore PodResourceStore,
	deviceResolver deviceResolver,
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator,
) (*Driver, error) {
	driver := &Driver{
		podNetworkKind:      podNetworkKind,
		driverName:          driverName,
		kubeClient:          kubeClient,
		podResourceStore:    podResourceStore,
		deviceResolver:      deviceResolver,
		deviceConfigurators: deviceConfigurators,
	}

	driverPluginPath := filepath.Join("/var/lib/kubelet/plugins/", driverName)

	err := os.MkdirAll(driverPluginPath, 0750)
	if err != nil {
		return nil, fmt.Errorf("failed to create plugin path %s: %v", driverPluginPath, err)
	}

	plugin, err := kubeletplugin.Start(
		ctx,
		driver,
		kubeletplugin.KubeClient(kubeClient),
		kubeletplugin.NodeName(nodeName),
		kubeletplugin.DriverName(driverName),
	)

	if err != nil {
		return nil, fmt.Errorf("start kubelet plugin: %w", err)
	}

	driver.draPlugin = plugin

	err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 30*time.Second, true, func(context.Context) (bool, error) {
		status := driver.draPlugin.RegistrationStatus()
		if status == nil {
			return false, nil
		}

		return status.PluginRegistered, nil
	})
	if err != nil {
		return nil, err
	}

	return driver, nil
}

// Stop the DRA Kubelet plugin.
func (d *Driver) Stop() {
	if d.draPlugin != nil {
		d.draPlugin.Stop()
	}
}

// PublishResources publishes the given resources via the DRA Kubelet plugin.
func (d *Driver) PublishResources(ctx context.Context, resources resourceslice.DriverResources) error {
	return d.draPlugin.PublishResources(ctx, resources)
}

// PrepareResourceClaims prepares the resource claims for the given pod by storing
// the claims in the podResourceStore and returning the devices allocated by this driver.
func (d *Driver) PrepareResourceClaims(ctx context.Context, claims []*resourcev1.ResourceClaim) (result map[types.UID]kubeletplugin.PrepareResult, err error) {
	result = make(map[types.UID]kubeletplugin.PrepareResult)
	for _, claim := range claims {
		devices, err := d.nodePrepareResource(ctx, claim)

		var claimResult kubeletplugin.PrepareResult
		if err != nil {
			klog.FromContext(ctx).Error(err, "error unpreparing ressources for a claim", "claim.Namespace", claim.Namespace, "claim.Name", claim.Name)
			claimResult.Err = err
		} else {
			claimResult.Devices = devices
		}

		result[claim.UID] = claimResult
	}
	return result, nil
}

// HandleError handles errors from the DRA Kubelet plugin.
func (d *Driver) HandleError(ctx context.Context, err error, msg string) {
	klog.FromContext(ctx).Error(err, "driver HandleError", "msg", msg)
}

// UnprepareResourceClaims unprepares the resource claims.
func (d *Driver) UnprepareResourceClaims(ctx context.Context, claims []kubeletplugin.NamespacedObject) (result map[types.UID]error, err error) {
	result = make(map[types.UID]error)

	for _, claimRef := range claims {
		uid := string(claimRef.UID)
		err := d.nodeUnprepareResource(ctx, uid)
		result[claimRef.UID] = err
	}

	return result, nil
}

func (d *Driver) nodeUnprepareResource(_ context.Context, _ string) error {
	// TODO
	return nil
}

func (d *Driver) nodePrepareResource(ctx context.Context, claim *resourcev1.ResourceClaim) ([]kubeletplugin.Device, error) {
	if len(claim.Status.ReservedFor) != 1 {
		return nil, fmt.Errorf("expected exactly one reservation for claim, got %d", len(claim.Status.ReservedFor))
	}

	if claim.Status.Allocation == nil {
		return nil, fmt.Errorf("claim status allocation is nil")
	}

	var devices []kubeletplugin.Device
	statusUpdates := &resourceapply.ResourceClaimStatusApplyConfiguration{Devices: []resourceapply.AllocatedDeviceStatusApplyConfiguration{}}

	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver != d.driverName {
			continue
		}

		resolvedDevice, err := d.deviceResolver.GetDevice(&result)
		if err != nil {
			return nil, fmt.Errorf("failed to get devices for claim during allocation: %v", err)
		}

		device, allocatedDeviceStatus, err := d.allocateDevice(ctx, resolvedDevice)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate device: %v", err)
		}

		if device == nil {
			return nil, fmt.Errorf("failed to allocate device: device is nil")
		}

		if allocatedDeviceStatus != nil {
			statusUpdates.WithDevices(allocatedDeviceStatus)
		}

		devices = append(devices, *device)
	}

	resourceClaimApply := resourceapply.ResourceClaim(claim.GetName(), claim.GetNamespace()).WithStatus(statusUpdates)
	updatedResourceClaim, err := d.kubeClient.ResourceV1().ResourceClaims(claim.GetNamespace()).ApplyStatus(
		ctx,
		resourceClaimApply,
		metav1.ApplyOptions{FieldManager: d.driverName, Force: true},
	)
	if err != nil {
		// todo: handle the error and rollback the allocation of the devices?
		return nil, fmt.Errorf("failed to update resource claim status: %v", err)
	}

	d.podResourceStore.Add(updatedResourceClaim.Status.ReservedFor[0].UID, updatedResourceClaim)

	klog.FromContext(ctx).Info("Devices for Claim", "claim.UID", claim.UID, "devices", devices)

	return devices, nil
}

func (d *Driver) allocateDevice(
	ctx context.Context,
	resolvedDevice *resolver.Device,
) (*kubeletplugin.Device, *resourceapply.AllocatedDeviceStatusApplyConfiguration, error) {
	if resolvedDevice == nil {
		return nil, nil, fmt.Errorf("resolved device is nil")
	}

	if resolvedDevice.DeviceRequestAllocationResult == nil {
		return nil, nil, fmt.Errorf("device request allocation result is nil for resolved device")
	}

	if resolvedDevice.ExposedDevice == nil {
		return nil, nil, fmt.Errorf("exposed device is nil for resolved device")
	}

	if resolvedDevice.DeviceConfiguration == nil {
		return nil, nil, fmt.Errorf("device configuration is nil for resolved device")
	}

	if resolvedDevice.DeviceNetwork == nil {
		return nil, nil, fmt.Errorf("device network is nil for resolved device")
	}

	allocatedDeviceStatus := &resourcev1.AllocatedDeviceStatus{
		Driver:      resolvedDevice.DeviceRequestAllocationResult.Driver,
		Pool:        resolvedDevice.DeviceRequestAllocationResult.Pool,
		Device:      resolvedDevice.DeviceRequestAllocationResult.Device,
		NetworkData: &resourcev1.NetworkDeviceData{},
	}
	if resolvedDevice.DeviceRequestAllocationResult.ShareID != nil {
		allocatedDeviceStatus.ShareID = (*string)(resolvedDevice.DeviceRequestAllocationResult.ShareID)
	}

	resourceClaimDeviceStatusData := &status.ResourceClaimDeviceStatusData{
		PodNetwork: &multinetworkv1alpha1.PodNetwork{
			Kind: d.podNetworkKind,
			Name: resolvedDevice.DeviceNetwork.Name,
		},
		Device:              resolvedDevice.HostDevice.DeepCopy(),
		DeviceConfiguration: resolvedDevice.DeviceConfiguration.DeepCopy(),
	}

	resultBytes, err := json.Marshal(resourceClaimDeviceStatusData)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to json.Marshal result (%v): %v", resourceClaimDeviceStatusData, err)
	}

	allocatedDeviceStatus.Data = &runtime.RawExtension{
		Raw: resultBytes,
	}

	deviceType := v1alpha1.GetDeviceType(*resolvedDevice.DeviceConfiguration)
	configurator, ok := d.deviceConfigurators[deviceType]
	if !ok {
		return nil, nil, fmt.Errorf("no configurator found for device type %s", deviceType)
	}

	allocatedDeviceStatus, err = configurator.Allocate(
		ctx,
		resolvedDevice.HostDevice,
		resolvedDevice.DeviceConfiguration,
		&resolvedDevice.DeviceNetwork.Spec.NetworkInterfaceConfiguration,
		allocatedDeviceStatus,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to allocate device: %v", err)
	}

	allocatedDeviceStatus.Conditions = []metav1.Condition{}
	meta.SetStatusCondition(&allocatedDeviceStatus.Conditions, metav1.Condition{
		Type:    status.DeviceStatusConditionAllocation,
		Status:  metav1.ConditionTrue,
		Reason:  status.DeviceStatusReasonAllocated,
		Message: "Device has been successfully allocated",
	})

	kubeletDevice := &kubeletplugin.Device{
		Requests:   []string{resolvedDevice.DeviceRequestAllocationResult.Request},
		PoolName:   resolvedDevice.DeviceRequestAllocationResult.Pool,
		DeviceName: resolvedDevice.DeviceRequestAllocationResult.Device,
		Metadata: &kubeletplugin.DeviceMetadata{
			Attributes:  map[string]resourcev1.DeviceAttribute{},
			NetworkData: allocatedDeviceStatus.NetworkData,
		},
	}

	for key, value := range resolvedDevice.ExposedDevice.Attributes {
		kubeletDevice.Metadata.Attributes[string(key)] = value
	}

	if resolvedDevice.DeviceRequestAllocationResult.ShareID != nil {
		kubeletDevice.ShareID = resolvedDevice.DeviceRequestAllocationResult.ShareID
	}

	resourceClaimStatusDevice := resourceapply.
		AllocatedDeviceStatus().
		WithDevice(allocatedDeviceStatus.Device).
		WithDriver(allocatedDeviceStatus.Driver).
		WithPool(allocatedDeviceStatus.Pool)
	if allocatedDeviceStatus.ShareID != nil {
		resourceClaimStatusDevice.WithShareID(string(*(allocatedDeviceStatus.ShareID)))
	}
	if allocatedDeviceStatus.Data != nil {
		resourceClaimStatusDevice.WithData(*allocatedDeviceStatus.Data)
	}
	if allocatedDeviceStatus.NetworkData != nil {
		networkDeviceDataApplyConfiguration := resourceapply.NetworkDeviceData().
			WithInterfaceName(allocatedDeviceStatus.NetworkData.InterfaceName).
			WithIPs(allocatedDeviceStatus.NetworkData.IPs...).
			WithHardwareAddress(allocatedDeviceStatus.NetworkData.HardwareAddress)
		resourceClaimStatusDevice.WithNetworkData(networkDeviceDataApplyConfiguration)
	}

	for _, condition := range allocatedDeviceStatus.Conditions {
		resourceClaimStatusDevice.WithConditions(
			metav1apply.Condition().
				WithType(condition.Type).
				WithReason(condition.Reason).
				WithStatus(condition.Status).
				WithLastTransitionTime(condition.LastTransitionTime),
		)
	}

	return kubeletDevice, resourceClaimStatusDevice, nil
}

func (d *Driver) WatchHealthStatus(ctx context.Context, reports chan<- kubeletplugin.DeviceHealthReport) error {
	return kubeletplugin.ErrHealthNotSupported
}
