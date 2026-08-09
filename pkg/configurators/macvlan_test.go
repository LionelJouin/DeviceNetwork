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

package configurators_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

var device0 = &resourcev1.AllocatedDeviceStatus{
	Driver:  "devicenetwork.io",
	Pool:    "pool0",
	Device:  "device0",
	ShareID: ptr.To("sharedID0"),
	NetworkData: &resourcev1.NetworkDeviceData{ // set the NetworkData to a non-nil value to avoid random data set during allocation.
		InterfaceName: "macvlan1",
	},
	Conditions: []metav1.Condition{},
}

func TestMacvlan_Allocate(t *testing.T) {
	deviceType := v1alpha1.DeviceTypeMacvlan
	tests := []struct {
		name                          string
		hostDevice                    *host.Device
		deviceConfiguration           *v1alpha1.DeviceConfiguration
		networkInterfaceConfiguration *v1alpha1.NetworkInterfaceConfiguration
		allocatedDeviceStatus         *resourcev1.AllocatedDeviceStatus
		want                          *resourcev1.AllocatedDeviceStatus
		wantErr                       bool
	}{
		{
			name:                  "default configuration",
			hostDevice:            &host.Device{Spec: host.DeviceSpec{InterfaceName: "net1"}},
			deviceConfiguration:   &v1alpha1.DeviceConfiguration{DeviceType: &deviceType},
			allocatedDeviceStatus: device0,
			want: func() *resourcev1.AllocatedDeviceStatus {
				allocatedDeviceStatus := device0.DeepCopy()
				allocatedDeviceStatus.Data = getRawExtension(&v1alpha1.ResourceClaimDeviceStatusData{
					Macvlan: &v1alpha1.MacvlanStatus{ParentName: "net1", ParentIndex: 0, Mode: int(netlink.MACVLAN_MODE_BRIDGE)},
				})
				return allocatedDeviceStatus
			}(),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mcvln configurators.Macvlan
			got, gotErr := mcvln.Allocate(t.Context(), tt.hostDevice, tt.deviceConfiguration, tt.networkInterfaceConfiguration, tt.allocatedDeviceStatus)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Allocate() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Allocate() succeeded unexpectedly")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Allocate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func getRawExtension(resourceClaimDeviceStatusData *v1alpha1.ResourceClaimDeviceStatusData) *kruntime.RawExtension {
	resultBytes, _ := json.Marshal(resourceClaimDeviceStatusData)
	return &kruntime.RawExtension{
		Raw: resultBytes,
	}
}

func TestMacvlan_ExposedDevice(t *testing.T) {
	one := resource.MustParse("1")

	tests := []struct {
		name       string
		hostDevice *host.Device
		device     *resourcev1.Device
		want       *resourcev1.Device
		wantErr    bool
	}{
		{
			name:       "default configuration",
			hostDevice: nil,
			device:     nil,
			want: &resourcev1.Device{
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{
					resourcev1.QualifiedName(v1alpha1.NetworkInterfaceCapacityMaxVirtualInterfaces): {
						Value: resource.MustParse(fmt.Sprintf("%d", v1alpha1.MaxVirtualDevices)),
						RequestPolicy: &resourcev1.CapacityRequestPolicy{
							Default:     &one,
							ValidValues: []resource.Quantity{one},
						},
					},
				},
				Attributes:               map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
				AllowMultipleAllocations: ptr.To(true),
			},
			wantErr: false,
		},
		{
			name:       "existing device",
			hostDevice: nil,
			device: &resourcev1.Device{
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{
					resourcev1.QualifiedName(v1alpha1.NetworkInterfaceCapacityMaxVirtualInterfaces): {
						Value: resource.MustParse("10"),
						RequestPolicy: &resourcev1.CapacityRequestPolicy{
							Default:     &one,
							ValidValues: []resource.Quantity{one},
						},
					},
				},
				Attributes:               map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
				AllowMultipleAllocations: ptr.To(false),
			},
			want: &resourcev1.Device{
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{
					resourcev1.QualifiedName(v1alpha1.NetworkInterfaceCapacityMaxVirtualInterfaces): {
						Value: resource.MustParse(fmt.Sprintf("%d", v1alpha1.MaxVirtualDevices)),
						RequestPolicy: &resourcev1.CapacityRequestPolicy{
							Default:     &one,
							ValidValues: []resource.Quantity{one},
						},
					},
				},
				Attributes:               map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
				AllowMultipleAllocations: ptr.To(true),
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mcvln configurators.Macvlan
			got, gotErr := mcvln.ExposedDevice(t.Context(), tt.hostDevice, tt.device)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("ExposedDevice() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("ExposedDevice() succeeded unexpectedly")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExposedDevice() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMacvlan_Configure(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root privileges (network namespace and link creation)")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := netns.Get()
	if err != nil {
		t.Fatalf("failed to get current netns: %v", err)
	}
	defer origNS.Close()
	defer netns.Set(origNS)

	// Create an isolated "host" namespace so the dummy parent never touches the real host.
	// netns.New() creates an unnamed namespace (fd-only, no /var/run/netns/ entry),
	// so closing the fd is enough for the kernel to clean it up.
	hostNS, err := netns.New()
	if err != nil {
		t.Fatalf("failed to create host netns: %v", err)
	}
	defer hostNS.Close()

	dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "dummy0"}}
	if err := netlink.LinkAdd(dummy); err != nil {
		t.Fatalf("failed to add dummy interface: %v", err)
	}

	parentLink, err := netlink.LinkByName("dummy0")
	if err != nil {
		t.Fatalf("failed to get dummy interface: %v", err)
	}
	parentIndex := parentLink.Attrs().Index

	// Create a named "pod" namespace.
	const nsName = "test-configure"
	podNS, err := netns.NewNamed(nsName)
	if err != nil {
		t.Fatalf("failed to create named netns: %v", err)
	}
	defer netns.DeleteNamed(nsName)
	defer podNS.Close()

	// netns.NewNamed switches to the new ns; switch back to the host ns
	// (where the parent interface lives and where Configure will run).
	if err := netns.Set(hostNS); err != nil {
		t.Fatalf("failed to switch to host netns: %v", err)
	}

	podNSPath := "/var/run/netns/" + nsName

	tests := []struct {
		name                  string
		podNetworkNamespace   string
		allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus
		want                  *resourcev1.AllocatedDeviceStatus
		wantErr               bool
	}{
		{
			name:                "default configuration",
			podNetworkNamespace: podNSPath,
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
				Data: getRawExtension(&v1alpha1.ResourceClaimDeviceStatusData{
					Macvlan: &v1alpha1.MacvlanStatus{ParentName: "dummy0", ParentIndex: parentIndex, Mode: int(netlink.MACVLAN_MODE_BRIDGE)},
				}),
				NetworkData: &resourcev1.NetworkDeviceData{
					InterfaceName: "macvlan1", HardwareAddress: "00:01:ec:84:fb:51", IPs: []string{"192.168.1.100/24", "fd00:db8:1::100/64"},
				},
				Conditions: []metav1.Condition{},
			},
			want: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
				Data: getRawExtension(&v1alpha1.ResourceClaimDeviceStatusData{
					Macvlan: &v1alpha1.MacvlanStatus{ParentName: "dummy0", ParentIndex: parentIndex, Mode: int(netlink.MACVLAN_MODE_BRIDGE)},
				}),
				NetworkData: &resourcev1.NetworkDeviceData{
					InterfaceName: "macvlan1", HardwareAddress: "00:01:ec:84:fb:51", IPs: []string{"192.168.1.100/24", "fd00:db8:1::100/64"},
				},
				Conditions: []metav1.Condition{},
			},
			wantErr: false,
		},
		{
			name:                "invalid namespace path",
			podNetworkNamespace: "/var/run/netns/nonexistent",
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0",
				Data: getRawExtension(&v1alpha1.ResourceClaimDeviceStatusData{
					Macvlan: &v1alpha1.MacvlanStatus{ParentName: "dummy0", ParentIndex: parentIndex, Mode: int(netlink.MACVLAN_MODE_BRIDGE)},
				}),
				NetworkData: &resourcev1.NetworkDeviceData{InterfaceName: "macvlan1"},
				Conditions:  []metav1.Condition{},
			},
			wantErr: true,
		},
		{
			name:                  "nil allocatedDeviceStatus",
			podNetworkNamespace:   podNSPath,
			allocatedDeviceStatus: nil,
			wantErr:               true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mcvln configurators.Macvlan
			got, gotErr := mcvln.Configure(t.Context(), tt.podNetworkNamespace, tt.allocatedDeviceStatus)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Configure() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Configure() succeeded unexpectedly")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Configure() = %v, want %v", got, tt.want)
			}

			// Verify the macvlan was actually created in the pod namespace.
			nlHandle, err := netlink.NewHandleAt(podNS)
			if err != nil {
				t.Fatalf("failed to create netlink handle in pod netns: %v", err)
			}
			defer nlHandle.Close()

			link, err := nlHandle.LinkByName(tt.allocatedDeviceStatus.NetworkData.InterfaceName)
			if err != nil {
				t.Fatalf("macvlan %q not found in pod namespace: %v", tt.allocatedDeviceStatus.NetworkData.InterfaceName, err)
			}
			if _, ok := link.(*netlink.Macvlan); !ok {
				t.Fatalf("link %q is %T, want *netlink.Macvlan", tt.allocatedDeviceStatus.NetworkData.InterfaceName, link)
			}
		})
	}
}
