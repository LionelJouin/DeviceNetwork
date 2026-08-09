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

// Package driver implements a DRA Kubelet plugin
// storing ResourceClaims of pods to later call CNI plugins
// on Pod creation.
package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/resolver"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// Driver represents a DRA Kubelet plugin.
type Driver struct {
	driverName          string
	kubeClient          kubernetes.Interface
	draPlugin           *kubeletplugin.Helper
	podResourceStore    PodResourceStore
	deviceResolver      *resolver.Resolver
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator
}

// Start the DRA Kubelet plugin.
func Start(
	ctx context.Context,
	driverName string,
	nodeName string,
	kubeClient kubernetes.Interface,
	podResourceStore PodResourceStore,
	deviceResolver *resolver.Resolver,
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator,
) (*Driver, error) {
	driver := &Driver{
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

func (d *Driver) nodePrepareResource(ctx context.Context, claim *resourcev1.ResourceClaim) ([]kubeletplugin.Device, error) {
	if len(claim.Status.ReservedFor) != 1 {
		return nil, fmt.Errorf("expected exactly one reservation for claim, got %d", len(claim.Status.ReservedFor))
	}

	updatedResourceClaim, err := d.setStatus(ctx, claim)
	if err != nil {
		return nil, fmt.Errorf("failed to set status for claim %s/%s: %v", claim.Namespace, claim.Name, err)
	}

	d.podResourceStore.Add(updatedResourceClaim.Status.ReservedFor[0].UID, updatedResourceClaim)

	var devices []kubeletplugin.Device

	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver != d.driverName {
			continue
		}

		device := kubeletplugin.Device{
			Requests:   []string{result.Request},
			PoolName:   result.Pool,
			DeviceName: result.Device,
		}
		devices = append(devices, device)
	}

	klog.FromContext(ctx).Info("Devices for Claim", "claim.UID", claim.UID, "devices", devices)

	return devices, nil
}

func (d *Driver) nodeUnprepareResource(_ context.Context, _ string) error {
	// TODO
	return nil
}

func (d *Driver) setStatus(
	ctx context.Context,
	claim *resourcev1.ResourceClaim,
) (*resourcev1.ResourceClaim, error) {
	resolvedDevices, err := d.deviceResolver.GetDevices(d.driverName, claim)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices for claim: %v", err)
	}

	errors := []error{}

	statusUpdates := &resourceapply.ResourceClaimStatusApplyConfiguration{Devices: []resourceapply.AllocatedDeviceStatusApplyConfiguration{}}

	for _, resolvedDevice := range resolvedDevices {
		if resolvedDevice.AllocatedDeviceStatus != nil {
			continue
		}

		if resolvedDevice.DeviceConfiguration == nil { // Should not happen
			klog.FromContext(ctx).Error(fmt.Errorf("device configuration is nil for resolved device, skipping"), "resolvedDevice", resolvedDevice)
			continue
		}

		deviceType := v1alpha1.GetDeviceType(*resolvedDevice.DeviceConfiguration)

		configurator, ok := d.deviceConfigurators[deviceType]
		if !ok {
			klog.FromContext(ctx).Error(fmt.Errorf("no configurator found for device type %s, skipping", deviceType), "resolvedDevice", resolvedDevice)
			continue
		}

		if resolvedDevice.AllocatedDeviceStatus == nil {
			resolvedDevice.AllocatedDeviceStatus = &resourcev1.AllocatedDeviceStatus{
				Driver: resolvedDevice.DeviceRequestAllocationResult.Driver,
				Pool:   resolvedDevice.DeviceRequestAllocationResult.Pool,
				Device: resolvedDevice.DeviceRequestAllocationResult.Device,
			}
			if resolvedDevice.DeviceRequestAllocationResult.ShareID != nil {
				resolvedDevice.AllocatedDeviceStatus.ShareID = (*string)(resolvedDevice.DeviceRequestAllocationResult.ShareID)
			}
		}

		resolvedDevice.AllocatedDeviceStatus, err = configurator.Allocate(
			ctx,
			resolvedDevice.HostDevice,
			resolvedDevice.DeviceConfiguration,
			&resolvedDevice.DeviceNetwork.Spec.NetworkInterfaceConfiguration,
			resolvedDevice.AllocatedDeviceStatus,
		)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to allocate device %s: %v", resolvedDevice, err))
			continue
		}

		resourceClaimStatusDevice := resourceapply.
			AllocatedDeviceStatus().
			WithDevice(resolvedDevice.AllocatedDeviceStatus.Device).
			WithDriver(resolvedDevice.AllocatedDeviceStatus.Driver).
			WithPool(resolvedDevice.AllocatedDeviceStatus.Pool)
		if resolvedDevice.AllocatedDeviceStatus.ShareID != nil {
			resourceClaimStatusDevice.WithShareID(string(*(resolvedDevice.AllocatedDeviceStatus.ShareID)))
		}
		if resolvedDevice.AllocatedDeviceStatus.Data != nil {
			resourceClaimStatusDevice.WithData(*resolvedDevice.AllocatedDeviceStatus.Data)
		}
		if resolvedDevice.AllocatedDeviceStatus.NetworkData != nil {
			networkDeviceDataApplyConfiguration := resourceapply.NetworkDeviceData().
				WithInterfaceName(resolvedDevice.AllocatedDeviceStatus.NetworkData.InterfaceName).
				WithIPs(resolvedDevice.AllocatedDeviceStatus.NetworkData.IPs...).
				WithHardwareAddress(resolvedDevice.AllocatedDeviceStatus.NetworkData.HardwareAddress)
			resourceClaimStatusDevice.WithNetworkData(networkDeviceDataApplyConfiguration)
		}

		for _, condition := range resolvedDevice.AllocatedDeviceStatus.Conditions {
			resourceClaimStatusDevice.WithConditions(
				metav1apply.Condition().
					WithType(condition.Type).
					WithReason(condition.Reason).
					WithStatus(condition.Status).
					WithLastTransitionTime(condition.LastTransitionTime),
			)
		}

		statusUpdates.WithDevices(resourceClaimStatusDevice)
	}

	resourceClaimApply := resourceapply.ResourceClaim(claim.GetName(), claim.GetNamespace()).WithStatus(statusUpdates)
	updatedResourceClaim, err := d.kubeClient.ResourceV1().ResourceClaims(claim.GetNamespace()).ApplyStatus(
		ctx,
		resourceClaimApply,
		metav1.ApplyOptions{FieldManager: d.driverName, Force: true},
	)
	if err != nil {
		// todo: handel the error and rollback the allocation of the devices?
		return nil, fmt.Errorf("failed to update resource claim status: %v", err)
	}

	return updatedResourceClaim, nil
}
