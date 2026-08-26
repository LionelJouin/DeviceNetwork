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
	"github.com/lioneljouin/devicenetwork/pkg/status"
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
			deviceConfiguration:   &v1alpha1.DeviceConfiguration{DeviceType: &deviceType, Macvlan: &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)}},
			allocatedDeviceStatus: device0,
			want: func() *resourcev1.AllocatedDeviceStatus {
				allocatedDeviceStatus := device0.DeepCopy()
				allocatedDeviceStatus.Data = getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{
						DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan), Macvlan: &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)},
					},
					Device: &host.Device{Spec: host.DeviceSpec{InterfaceName: "net1", InterfaceIndex: 0}},
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

func getRawExtension(resourceClaimDeviceStatusData *status.ResourceClaimDeviceStatusData) *kruntime.RawExtension {
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
			hostDevice: &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth0"}},
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
				ConsumesCounters: []resourcev1.DeviceCounterConsumption{
					{CounterSet: "eth0", Counters: map[string]resourcev1.Counter{"mutual-exclusion": {Value: one}}},
				},
			},
			wantErr: false,
		},
		{
			name:       "existing device",
			hostDevice: &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth0"}},
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
				ConsumesCounters: []resourcev1.DeviceCounterConsumption{
					{CounterSet: "eth0", Counters: map[string]resourcev1.Counter{"mutual-exclusion": {Value: one}}},
				},
			},
			wantErr: false,
		},
		{
			name:       "nil hostDevice",
			hostDevice: nil,
			device:     nil,
			wantErr:    true,
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
	t.Cleanup(func() {
		if err := netns.Set(origNS); err != nil {
			t.Errorf("failed to restore netns: %v", err)
		}
		if err := origNS.Close(); err != nil {
			t.Errorf("failed to close original netns: %v", err)
		}
	})

	// Create an isolated "host" namespace so the dummy parent never touches the real host.
	// netns.New() creates an unnamed namespace (fd-only, no /var/run/netns/ entry),
	// so closing the fd is enough for the kernel to clean it up.
	hostNS, err := netns.New()
	if err != nil {
		t.Fatalf("failed to create host netns: %v", err)
	}
	t.Cleanup(func() {
		if err := hostNS.Close(); err != nil {
			t.Errorf("failed to close host netns: %v", err)
		}
	})

	dummy := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "dummy0"}}
	if err := netlink.LinkAdd(dummy); err != nil {
		t.Fatalf("failed to add dummy interface: %v", err)
	}

	parentLink, err := netlink.LinkByName("dummy0")
	if err != nil {
		t.Fatalf("failed to get dummy interface: %v", err)
	}
	parentIndex := parentLink.Attrs().Index

	const nsName = "test-configure"
	if err := netns.DeleteNamed(nsName); err != nil {
		t.Logf("cleanup of previous netns %q: %v (may not exist)", nsName, err)
	}
	podNS, err := netns.NewNamed(nsName)
	if err != nil {
		t.Fatalf("failed to create named netns: %v", err)
	}
	t.Cleanup(func() {
		if err := podNS.Close(); err != nil {
			t.Errorf("failed to close pod netns: %v", err)
		}
		if err := netns.DeleteNamed(nsName); err != nil {
			t.Errorf("failed to delete named netns %q: %v", nsName, err)
		}
	})

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
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{
						DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan), Macvlan: &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)},
					},
					Device: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dummy0", InterfaceIndex: parentIndex}},
				}),
				NetworkData: &resourcev1.NetworkDeviceData{
					InterfaceName: "macvlan1", HardwareAddress: "00:01:ec:84:fb:51", IPs: []string{"192.168.1.100/24", "fd00:db8:1::100/64"},
				},
				Conditions: []metav1.Condition{},
			},
			want: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{
						DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan), Macvlan: &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)},
					},
					Device: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dummy0", InterfaceIndex: parentIndex}},
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
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{
						DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan), Macvlan: &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)},
					},
					Device: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dummy0", InterfaceIndex: parentIndex}},
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
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			if err := netns.Set(hostNS); err != nil {
				t.Fatalf("failed to set netns: %v", err)
			}

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

func TestMacvlan_IsSupported(t *testing.T) {
	tests := []struct {
		name                string
		hostDevice          *host.Device
		deviceConfiguration *v1alpha1.DeviceConfiguration
		want                bool
		wantErr             bool
		requiresRoot        bool
	}{
		{
			name:                "hostDevice is nil",
			hostDevice:          nil,
			deviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan)},
			wantErr:             true,
		},
		{
			name:                "deviceConfiguration is nil",
			hostDevice:          &host.Device{Spec: host.DeviceSpec{InterfaceName: "eth0"}},
			deviceConfiguration: nil,
			wantErr:             true,
		},
		{
			name:                "device type is not macvlan",
			hostDevice:          &host.Device{Spec: host.DeviceSpec{InterfaceName: "eth0"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice)},
			want:                false,
		},
		{
			name:       "invalid macvlan mode",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "eth0"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan),
				Macvlan:    &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanMode("invalid"))},
			},
			want: false,
		},
		{
			name:       "host device does not exist",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-missing"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan),
				Macvlan:    &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)},
			},
			wantErr: true,
		},
		{
			name:       "lower device is not ethernet",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "lo"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan),
				Macvlan:    &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)},
			},
			want: false,
		},
		{
			name:       "device is enslaved to a bridge",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-slave"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan),
				Macvlan:    &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)},
			},
			want:         false,
			requiresRoot: true,
		},
		{
			name:       "ethernet device is supported",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-eth"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan),
				Macvlan:    &v1alpha1.Macvlan{Mode: ptr.To(v1alpha1.MacvlanModeBridge)},
			},
			want:         true,
			requiresRoot: true,
		},
	}

	// The dn-test-eth/dn-test-slave/dn-test-br0 interfaces are only needed by
	// the requiresRoot cases; set them up in an isolated netns so the real
	// host is never touched.
	var testNS netns.NsHandle
	if os.Getuid() == 0 {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		origNS, err := netns.Get()
		if err != nil {
			t.Fatalf("failed to get current netns: %v", err)
		}
		t.Cleanup(func() {
			if err := netns.Set(origNS); err != nil {
				t.Errorf("failed to restore netns: %v", err)
			}
			if err := origNS.Close(); err != nil {
				t.Errorf("failed to close original netns: %v", err)
			}
		})

		testNS, err = netns.New()
		if err != nil {
			t.Fatalf("failed to create test netns: %v", err)
		}
		t.Cleanup(func() {
			if err := testNS.Close(); err != nil {
				t.Errorf("failed to close test netns: %v", err)
			}
		})

		eth := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "dn-test-eth"}}
		if err := netlink.LinkAdd(eth); err != nil {
			t.Fatalf("failed to add dummy interface: %v", err)
		}

		bridge := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: "dn-test-br0"}}
		if err := netlink.LinkAdd(bridge); err != nil {
			t.Fatalf("failed to add bridge: %v", err)
		}

		slave := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "dn-test-slave"}}
		if err := netlink.LinkAdd(slave); err != nil {
			t.Fatalf("failed to add slave interface: %v", err)
		}
		if err := netlink.LinkSetMaster(slave, bridge); err != nil {
			t.Fatalf("failed to enslave interface to bridge: %v", err)
		}

		// Switch this thread back to the original ns; each requiresRoot
		// subtest below switches into testNS for itself since t.Run executes
		// subtests on a new goroutine, which is not guaranteed to run on the
		// OS thread that was just switched into testNS above.
		if err := netns.Set(origNS); err != nil {
			t.Fatalf("failed to switch back to the original netns: %v", err)
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.requiresRoot {
				if os.Getuid() != 0 {
					t.Skip("requires root privileges (network namespace and link creation)")
				}

				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				if err := netns.Set(testNS); err != nil {
					t.Fatalf("failed to set netns: %v", err)
				}
			}

			var mcvln configurators.Macvlan
			got, gotErr := mcvln.IsSupported(t.Context(), tt.hostDevice, tt.deviceConfiguration)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("IsSupported() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("IsSupported() succeeded unexpectedly")
			}
			if got != tt.want {
				t.Errorf("IsSupported() = %v, want %v", got, tt.want)
			}
		})
	}
}
