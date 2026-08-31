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

package configurators

import (
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	"github.com/lioneljouin/devicenetwork/pkg/status"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

var hostDeviceDevice0 = &resourcev1.AllocatedDeviceStatus{
	Driver:  "devicenetwork.io",
	Pool:    "pool0",
	Device:  "device0",
	ShareID: ptr.To("sharedID0"),
	NetworkData: &resourcev1.NetworkDeviceData{ // set the NetworkData to a non-nil value to avoid random data set during allocation.
		InterfaceName: "net1",
	},
	Conditions: []metav1.Condition{},
}

func TestHostDevice_IsSupported(t *testing.T) {
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
			deviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice)},
			wantErr:             true,
		},
		{
			name:                "deviceConfiguration is nil",
			hostDevice:          &host.Device{Spec: host.DeviceSpec{InterfaceName: "eth0"}},
			deviceConfiguration: nil,
			wantErr:             true,
		},
		{
			name:                "device type is not HostDevice",
			hostDevice:          &host.Device{Spec: host.DeviceSpec{InterfaceName: "eth0"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan)},
			want:                false,
		},
		{
			name:       "loopback device is not supported",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "lo"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
			},
			want: false,
		},
		{
			name:       "device is enslaved to a bridge",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-slave"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
			},
			want:         false,
			requiresRoot: true,
		},
		{
			name:       "ethernet device is supported",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-eth"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
			},
			want:         true,
			requiresRoot: true,
		},
		{
			name:       "bridge device is not supported (NETIF_F_NETNS_LOCAL)",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-br0"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
			},
			want:         false,
			requiresRoot: true,
		},
		{
			name:       "tun device is not supported (fd-based)",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-tun"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
			},
			want:         false,
			requiresRoot: true,
		},
		{
			name:       "macvlan device is not supported (parent-dependent, destroyed on netns deletion)",
			hostDevice: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-mcvln"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{
				DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
			},
			want:         false,
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

		tun := &netlink.Tuntap{LinkAttrs: netlink.LinkAttrs{Name: "dn-test-tun"}, Mode: netlink.TUNTAP_MODE_TUN}
		if err := netlink.LinkAdd(tun); err != nil {
			t.Fatalf("failed to add tun interface: %v", err)
		}

		ethLink, err := netlink.LinkByName("dn-test-eth")
		if err != nil {
			t.Fatalf("failed to get dn-test-eth: %v", err)
		}
		mcvln := &netlink.Macvlan{
			LinkAttrs: netlink.LinkAttrs{Name: "dn-test-mcvln", ParentIndex: ethLink.Attrs().Index},
			Mode:      netlink.MACVLAN_MODE_BRIDGE,
		}
		if err := netlink.LinkAdd(mcvln); err != nil {
			t.Fatalf("failed to add macvlan interface: %v", err)
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

			hd := HostDevice{}
			got, gotErr := hd.IsSupported(t.Context(), tt.hostDevice, tt.deviceConfiguration)
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

func TestHostDevice_ExposedDevice(t *testing.T) {
	max := resource.MustParse("65536")

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
				Attributes:               map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
				Capacity:                 map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{},
				AllowMultipleAllocations: ptr.To(false),
				ConsumesCounters: []resourcev1.DeviceCounterConsumption{
					{CounterSet: "eth0", Counters: map[string]resourcev1.Counter{"mutual-exclusion": {Value: max}}},
				},
			},
			wantErr: false,
		},
		{
			name:       "existing device",
			hostDevice: &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth0"}},
			device: &resourcev1.Device{
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{
					"other-capacity": {Value: resource.MustParse("10")},
				},
				Attributes:               map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
				AllowMultipleAllocations: ptr.To(true),
			},
			want: &resourcev1.Device{
				Capacity: map[resourcev1.QualifiedName]resourcev1.DeviceCapacity{
					"other-capacity": {Value: resource.MustParse("10")},
				},
				Attributes:               map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
				AllowMultipleAllocations: ptr.To(false),
				ConsumesCounters: []resourcev1.DeviceCounterConsumption{
					{CounterSet: "eth0", Counters: map[string]resourcev1.Counter{"mutual-exclusion": {Value: max}}},
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
			var hd HostDevice
			got, gotErr := hd.ExposedDevice(t.Context(), tt.hostDevice, tt.device)
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

func TestHostDevice_Allocate(t *testing.T) {
	deviceType := v1alpha1.DeviceTypeHostDevice

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
			allocatedDeviceStatus: hostDeviceDevice0,
			want: func() *resourcev1.AllocatedDeviceStatus {
				allocatedDeviceStatus := hostDeviceDevice0.DeepCopy()
				allocatedDeviceStatus.Data = getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice)},
					Device:              &host.Device{Spec: host.DeviceSpec{InterfaceName: "net1"}},
				})
				return allocatedDeviceStatus
			}(),
			wantErr: false,
		},
		{
			name:                "network data defaults to the host device interface name when unset",
			hostDevice:          &host.Device{Spec: host.DeviceSpec{InterfaceName: "net1"}},
			deviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: &deviceType},
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
				Conditions: []metav1.Condition{},
			},
			want: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
				NetworkData: &resourcev1.NetworkDeviceData{InterfaceName: "net1"},
				Conditions:  []metav1.Condition{},
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice)},
					Device:              &host.Device{Spec: host.DeviceSpec{InterfaceName: "net1"}},
				}),
			},
			wantErr: false,
		},
		{
			name:                  "nil allocatedDeviceStatus",
			hostDevice:            &host.Device{Spec: host.DeviceSpec{InterfaceName: "net1"}},
			deviceConfiguration:   &v1alpha1.DeviceConfiguration{DeviceType: &deviceType},
			allocatedDeviceStatus: nil,
			wantErr:               true,
		},
		{
			name:                  "nil deviceConfiguration",
			hostDevice:            &host.Device{Spec: host.DeviceSpec{InterfaceName: "net1"}},
			deviceConfiguration:   nil,
			allocatedDeviceStatus: hostDeviceDevice0,
			wantErr:               true,
		},
		{
			name:                  "nil hostDevice",
			hostDevice:            nil,
			deviceConfiguration:   &v1alpha1.DeviceConfiguration{DeviceType: &deviceType},
			allocatedDeviceStatus: hostDeviceDevice0,
			wantErr:               true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hd HostDevice
			got, gotErr := hd.Allocate(t.Context(), tt.hostDevice, tt.deviceConfiguration, tt.networkInterfaceConfiguration, tt.allocatedDeviceStatus)
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

func TestHostDevice_Configure(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("requires root privileges (network namespace and link creation)")
	}

	// Lock the setup goroutine to its OS thread so that netns switches take
	// effect for all netlink calls below. Each subtest re-locks independently.
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

	// hostNS is an isolated namespace where all test interfaces live, so the
	// real host network stack is never touched.
	hostNS, err := netns.New()
	if err != nil {
		t.Fatalf("failed to create host netns: %v", err)
	}
	t.Cleanup(func() {
		if err := hostNS.Close(); err != nil {
			t.Errorf("failed to close host netns: %v", err)
		}
	})

	// dn-test-eth: plain dummy used by the basic Configure test cases.
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "dn-test-eth"}}); err != nil {
		t.Fatalf("failed to add dummy interface: %v", err)
	}

	// dn-test-rdma: dummy for the RDMA error-path test. It gets stranded in
	// podNS when RdmaLinkByName fails, so it must not share a name with other
	// test cases.
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "dn-test-rdma"}}); err != nil {
		t.Fatalf("failed to add RDMA dummy interface: %v", err)
	}

	// dn-test-srdma: dummy for the full software RDMA test. It is brought up
	// so that rxe/siw drivers can attach to it. rdmaDevName is empty if neither
	// module is available, which causes the test case to skip.
	const softRDMAIf = "dn-test-srdma"
	if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: softRDMAIf}}); err != nil {
		t.Fatalf("failed to add software RDMA dummy: %v", err)
	}
	srdmaLink, err := netlink.LinkByName(softRDMAIf)
	if err != nil {
		t.Fatalf("failed to get software RDMA dummy: %v", err)
	}
	if err := netlink.LinkSetUp(srdmaLink); err != nil {
		t.Fatalf("failed to bring up software RDMA dummy: %v", err)
	}

	// Snapshot existing RDMA devices before attaching a software RDMA driver.
	// The kernel assigns its own device name (e.g. rxe0) and may ignore the
	// name passed to RdmaLinkAdd, so we diff before/after to find the real one.
	rdmaLinksBefore, _ := netlink.RdmaLinkList()
	rdmaDevsBefore := map[string]bool{}
	for _, l := range rdmaLinksBefore {
		rdmaDevsBefore[l.Attrs.Name] = true
	}

	var rdmaDevName string
	for _, driverType := range []string{"rxe", "siw"} {
		if err := netlink.RdmaLinkAdd("", driverType, softRDMAIf); err != nil {
			continue
		}
		rdmaLinksAfter, err := netlink.RdmaLinkList()
		if err != nil {
			t.Fatalf("failed to list RDMA links after creation: %v", err)
		}
		for _, l := range rdmaLinksAfter {
			if !rdmaDevsBefore[l.Attrs.Name] {
				rdmaDevName = l.Attrs.Name
				break
			}
		}
		t.Cleanup(func() { _ = netlink.RdmaLinkDel(rdmaDevName) })
		break
	}

	// Build a fake sysfsRoot for the software RDMA interface. Dummies have no
	// PCI device/ symlink in sysfs, so the real infiniband/ path never exists
	// even with a software RDMA device attached.
	softRDMASysfsRoot := t.TempDir()
	if rdmaDevName != "" {
		ibPath := softRDMASysfsRoot + "/" + softRDMAIf + "/device/infiniband/" + rdmaDevName
		if err := os.MkdirAll(ibPath, 0o755); err != nil {
			t.Fatalf("failed to create software RDMA sysfs: %v", err)
		}
	}

	// podNS is a named namespace so it has a path under /var/run/netns/ that
	// can be passed to Configure as the pod network namespace.
	const nsName = "test-hd-configure"
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

	// Switch back to hostNS; netns.NewNamed leaves the thread in the new ns.
	if err := netns.Set(hostNS); err != nil {
		t.Fatalf("failed to switch to host netns: %v", err)
	}

	podNSPath := "/var/run/netns/" + nsName

	hostDeviceStatusData := func(ifName string) *resourcev1.AllocatedDeviceStatus {
		return &resourcev1.AllocatedDeviceStatus{
			Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
			NetworkData: &resourcev1.NetworkDeviceData{InterfaceName: ifName},
			Conditions:  []metav1.Condition{},
			Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
				DeviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice)},
				Device:              &host.Device{Spec: host.DeviceSpec{InterfaceName: ifName}},
			}),
		}
	}

	rdmaSysfsRoot := t.TempDir()
	if err := os.MkdirAll(rdmaSysfsRoot+"/dn-test-rdma/device/infiniband/mlx5_0", 0o755); err != nil {
		t.Fatalf("failed to create fake RDMA sysfs: %v", err)
	}

	tests := []struct {
		name                  string
		podNetworkNamespace   string
		allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus
		sysfsRoot             string
		skipReason            string
		want                  *resourcev1.AllocatedDeviceStatus
		wantErr               bool
		// verifyRDMAGone checks the RDMA device is no longer in the host netns after a successful Configure.
		verifyRDMAGone string
	}{
		{
			name:                  "nil allocatedDeviceStatus",
			podNetworkNamespace:   podNSPath,
			allocatedDeviceStatus: nil,
			wantErr:               true,
		},
		{
			name:                "nil data",
			podNetworkNamespace: podNSPath,
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0",
			},
			wantErr: true,
		},
		{
			name:                "nil device in status data",
			podNetworkNamespace: podNSPath,
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice)},
				}),
			},
			wantErr: true,
		},
		{
			name:                "nil device configuration in status data",
			podNetworkNamespace: podNSPath,
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					Device: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-eth"}},
				}),
			},
			wantErr: true,
		},
		{
			name:                "wrong device type",
			podNetworkNamespace: podNSPath,
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: ptr.To(v1alpha1.DeviceTypeMacvlan)},
					Device:              &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-eth"}},
				}),
			},
			wantErr: true,
		},
		{
			name:                  "invalid namespace path",
			podNetworkNamespace:   "/var/run/netns/nonexistent",
			allocatedDeviceStatus: hostDeviceStatusData("dn-test-eth"),
			wantErr:               true,
		},
		{
			name:                  "interface not found",
			podNetworkNamespace:   podNSPath,
			allocatedDeviceStatus: hostDeviceStatusData("nonexistent"),
			wantErr:               true,
		},
		{
			// Interface moves first; RdmaLinkByName then fails because dn-test-rdma
			// is a dummy with no real RDMA device. Exercises the RDMA branch without hardware.
			name:                "RDMA enabled, fails at RdmaLinkByName on non-RDMA interface",
			podNetworkNamespace: podNSPath,
			sysfsRoot:           rdmaSysfsRoot,
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
				NetworkData: &resourcev1.NetworkDeviceData{InterfaceName: "dn-test-rdma"},
				Conditions:  []metav1.Condition{},
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{
						DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
					},
					Device: &host.Device{Spec: host.DeviceSpec{InterfaceName: "dn-test-rdma"}},
				}),
			},
			wantErr: true,
		},
		{
			name:                  "moves interface to pod network namespace",
			podNetworkNamespace:   podNSPath,
			allocatedDeviceStatus: hostDeviceStatusData("dn-test-eth"),
			want:                  hostDeviceStatusData("dn-test-eth"),
			wantErr:               false,
		},
		{
			name:                "RDMA enabled, moves interface and RDMA device via software RDMA",
			podNetworkNamespace: podNSPath,
			sysfsRoot:           softRDMASysfsRoot,
			skipReason: func() string {
				if rdmaDevName == "" {
					return "neither rdma_rxe nor rdma_siw is available in this kernel"
				}
				return ""
			}(),
			allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
				NetworkData: &resourcev1.NetworkDeviceData{InterfaceName: softRDMAIf},
				Conditions:  []metav1.Condition{},
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{
						DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
					},
					Device: &host.Device{Spec: host.DeviceSpec{InterfaceName: softRDMAIf}},
				}),
			},
			want: &resourcev1.AllocatedDeviceStatus{
				Driver: "devicenetwork.io", Pool: "pool0", Device: "device0", ShareID: ptr.To("sharedID0"),
				NetworkData: &resourcev1.NetworkDeviceData{InterfaceName: softRDMAIf},
				Conditions:  []metav1.Condition{},
				Data: getRawExtension(&status.ResourceClaimDeviceStatusData{
					DeviceConfiguration: &v1alpha1.DeviceConfiguration{
						DeviceType: ptr.To(v1alpha1.DeviceTypeHostDevice),
					},
					Device: &host.Device{Spec: host.DeviceSpec{InterfaceName: softRDMAIf}},
				}),
			},
			verifyRDMAGone: rdmaDevName,
			wantErr:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipReason != "" {
				t.Skip(tt.skipReason)
			}

			// Each subtest locks its goroutine to an OS thread and switches into
			// hostNS so that netlink calls operate on the right namespace.
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			if err := netns.Set(hostNS); err != nil {
				t.Fatalf("failed to set netns: %v", err)
			}

			hd := HostDevice{sysfsNetRoot: tt.sysfsRoot}
			got, gotErr := hd.Configure(t.Context(), tt.podNetworkNamespace, tt.allocatedDeviceStatus)
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

			// Verify the network interface was moved into the pod namespace and
			// is no longer visible from the host namespace (LinkByName uses the
			// current netns, which is still hostNS at this point).
			nlHandle, err := netlink.NewHandleAt(podNS)
			if err != nil {
				t.Fatalf("failed to create netlink handle in pod netns: %v", err)
			}
			defer nlHandle.Close()

			ifName := tt.allocatedDeviceStatus.NetworkData.InterfaceName
			if _, err := nlHandle.LinkByName(ifName); err != nil {
				t.Errorf("interface %q not found in pod netns after Configure: %v", ifName, err)
			}

			// For RDMA-enabled cases, verify the RDMA device also moved by
			// confirming it is no longer visible from the host namespace.
			if tt.verifyRDMAGone != "" {
				if _, err := netlink.RdmaLinkByName(tt.verifyRDMAGone); err == nil {
					t.Errorf("RDMA device %q still visible in host netns after Configure", tt.verifyRDMAGone)
				}
			}
		})
	}
}
