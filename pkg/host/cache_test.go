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

package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jaypipes/ghw"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/vishvananda/netlink"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
)

// TestMain re-execs the entire test binary inside an isolated network + mount
// namespace when running as root. This ensures that all goroutines (including
// those spawned by DeviceCache.Run) inherit the namespace, and that all test
// flags — including -test.coverprofile — are forwarded to the child process so
// coverage data is not lost. The parent exits via os.Exit, which bypasses its
// own (empty) coverage dump, leaving the child's coverage file for go test to
// collect.
func TestMain(m *testing.M) {
	if os.Getuid() == 0 && os.Getenv("IN_TEST_NETNS") == "" {
		cmd := exec.Command(os.Args[0], os.Args[1:]...)
		cmd.Env = append(os.Environ(), "IN_TEST_NETNS=1")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags: syscall.CLONE_NEWNET | syscall.CLONE_NEWNS | syscall.CLONE_NEWCGROUP,
		}
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				os.Exit(exitErr.ExitCode())
			}
			fmt.Fprintf(os.Stderr, "re-exec in netns failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if os.Getenv("IN_TEST_NETNS") != "" {
		// Make all mounts in this new mount namespace private and non-recursive
		// so that the sysfs/cgroupfs (re)mounts below cannot propagate back to
		// the host. On systemd hosts "/" is mounted shared, which a new mount
		// namespace inherits; without this the mounts would leak to the host and
		// corrupt its /sys and /sys/fs/cgroup.
		if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
			fmt.Fprintf(os.Stderr, "failed to make mounts private: %v\n", err)
			os.Exit(1)
		}

		// Remount sysfs so /sys/class/net reflects the new network namespace.
		if err := syscall.Unmount("/sys", syscall.MNT_DETACH); err != nil {
			fmt.Fprintf(os.Stderr, "failed to unmount /sys: %v\n", err)
			os.Exit(1)
		}
		if err := syscall.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
			fmt.Fprintf(os.Stderr, "failed to mount sysfs: %v\n", err)
			os.Exit(1)
		}

		// Mount a fresh cgroupfs in the new cgroup namespace (CLONE_NEWCGROUP)
		// so the Go runtime and test framework can access cgroup files without
		// touching or depending on the host cgroup hierarchy.
		if err := os.MkdirAll("/sys/fs/cgroup", 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create /sys/fs/cgroup: %v\n", err)
			os.Exit(1)
		}
		if err := syscall.Mount("cgroup2", "/sys/fs/cgroup", "cgroup2", 0, ""); err != nil {
			// cgroup v1 host: mount a tmpfs so the path exists and is accessible.
			if err := syscall.Mount("tmpfs", "/sys/fs/cgroup", "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV|syscall.MS_NOEXEC, "mode=755"); err != nil {
				fmt.Fprintf(os.Stderr, "failed to mount cgroupfs: %v\n", err)
				os.Exit(1)
			}
		}
	}

	os.Exit(m.Run())
}

func newDeviceCache(
	t *testing.T,
	initialDeviceObjects []runtime.Object,
) *DeviceCache {
	t.Helper()

	deviceCache, err := NewDeviceCache(time.Hour)
	if err != nil {
		t.Fatal("NewDeviceCache failed:", err)
	}

	for _, d := range initialDeviceObjects {
		if err := deviceCache.Informer().GetStore().Add(d); err != nil {
			t.Fatalf("failed to add device to cache: %v", err)
		}
	}

	return deviceCache
}

func sortDevices(devices []*Device) {
	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Name < devices[j].Name
	})
}

func TestEventHandlers(t *testing.T) {
	if os.Getenv("IN_TEST_NETNS") == "" {
		t.Skip("requires root privileges (network namespace and link creation)")
	}

	// Add dummy interfaces into the isolated namespace (created by TestMain)
	// so the DeviceCache discovers them alongside the loopback. The dntst
	// prefix avoids collisions with any pre-existing interface names.
	dummyNames := []string{"dntst0", "dntst1"}
	for _, name := range dummyNames {
		if err := netlink.LinkAdd(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name}}); err != nil {
			t.Fatalf("failed to add %s: %v", name, err)
		}
	}

	// dntst0 is the interface used to exercise RDMA discovery through the cache.
	const rdmaCapableIf = "dntst0"

	// rdmaSysfsRoot exposes an RDMA device under device/infiniband for dntst0;
	// plainSysfsRoot exposes none, so the same interface is not RDMA-capable.
	rdmaSysfsRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rdmaSysfsRoot, rdmaCapableIf, "device", "infiniband", "mlx5_0"), 0o755); err != nil {
		t.Fatalf("failed to create fake RDMA sysfs: %v", err)
	}
	plainSysfsRoot := t.TempDir()

	// Build the expected device list from whatever ghw sees in this namespace
	// (loopback + the two dummies above). rdmaCapable marks whether dntst0 is
	// expected to be reported as RDMA-capable.
	netInfo, err := ghw.Network()
	if err != nil {
		t.Fatalf("failed to get network info: %v", err)
	}

	expectedDevices := func(rdmaCapable bool) []*Device {
		var devices []*Device
		for _, nic := range netInfo.NICs {
			link, err := netlink.LinkByName(nic.Name)
			if err != nil {
				t.Fatalf("failed to get link %s: %v", nic.Name, err)
			}
			devices = append(devices, &Device{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Device",
					APIVersion: v1alpha1.GroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{Name: nic.Name},
				Spec: DeviceSpec{
					InterfaceName:  nic.Name,
					InterfaceIndex: link.Attrs().Index,
					RDMACapable:    rdmaCapable && nic.Name == rdmaCapableIf,
				},
			})
		}
		sortDevices(devices)
		return devices
	}

	tests := []struct {
		name            string
		sysfsNetRoot    string
		expectedDevices []*Device
	}{
		{
			name:            "RDMA-capable NIC is discovered as RDMA-capable",
			sysfsNetRoot:    rdmaSysfsRoot,
			expectedDevices: expectedDevices(true),
		},
		{
			name:            "NIC without an RDMA device is not RDMA-capable",
			sysfsNetRoot:    plainSysfsRoot,
			expectedDevices: expectedDevices(false),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			dc, err := NewDeviceCache(1 * time.Millisecond)
			if err != nil {
				t.Fatal("NewDeviceCache failed:", err)
			}
			dc.sysfsNetRoot = tt.sysfsNetRoot

			go func() {
				_ = dc.Run(ctx, 1)
			}()

			if err := wait.PollUntilContextTimeout(ctx, 1*time.Millisecond, 2*time.Second, true, func(ctx context.Context) (bool, error) {
				got := dc.List(ctx)
				sortDevices(got)
				return reflect.DeepEqual(got, tt.expectedDevices), nil
			}); err != nil {
				got := dc.List(ctx)
				sortDevices(got)
				t.Errorf("List() mismatch (-want +got):\n%s", cmp.Diff(tt.expectedDevices, got))
			}
		})
	}
}

func TestDeviceCache_List(t *testing.T) {
	eth0 := &Device{
		ObjectMeta: metav1.ObjectMeta{Name: "eth0"},
		Spec:       DeviceSpec{InterfaceName: "eth0", RDMACapable: true},
	}
	eth1 := &Device{
		ObjectMeta: metav1.ObjectMeta{Name: "eth1"},
		Spec:       DeviceSpec{InterfaceName: "eth1"},
	}
	eno1 := &Device{
		ObjectMeta: metav1.ObjectMeta{Name: "eno1"},
		Spec:       DeviceSpec{InterfaceName: "eno1"},
	}

	tests := []struct {
		name                 string
		initialDeviceObjects []runtime.Object
		opts                 []Option
		want                 []*Device
	}{
		{
			name:                 "list all devices without options",
			initialDeviceObjects: []runtime.Object{eth0, eth1, eno1},
			want:                 []*Device{eno1, eth0, eth1},
		},
		{
			name: "empty cache returns empty",
			want: []*Device{},
		},
		{
			name:                 "CEL selector filters matching devices",
			initialDeviceObjects: []runtime.Object{eth0, eth1, eno1},
			opts: []Option{
				WithSelectors([]v1alpha1.Selector{
					{CEL: &v1alpha1.CELDeviceSelector{Expression: `interfaceName.startsWith("eth")`}},
				}),
			},
			want: []*Device{eth0, eth1},
		},
		{
			name:                 "CEL selector filters RDMA-capable devices",
			initialDeviceObjects: []runtime.Object{eth0, eth1, eno1},
			opts: []Option{
				WithSelectors([]v1alpha1.Selector{
					{CEL: &v1alpha1.CELDeviceSelector{Expression: `rdmaCapable`}},
				}),
			},
			want: []*Device{eth0},
		},
		{
			name:                 "CEL selector no match returns empty",
			initialDeviceObjects: []runtime.Object{eth0, eth1},
			opts: []Option{
				WithSelectors([]v1alpha1.Selector{
					{CEL: &v1alpha1.CELDeviceSelector{Expression: `interfaceName == "eno1"`}},
				}),
			},
			want: []*Device{},
		},
		{
			name:                 "CEL selector exact match",
			initialDeviceObjects: []runtime.Object{eth0, eth1, eno1},
			opts: []Option{
				WithSelectors([]v1alpha1.Selector{
					{CEL: &v1alpha1.CELDeviceSelector{Expression: `interfaceName == "eth0"`}},
				}),
			},
			want: []*Device{eth0},
		},
		{
			name:                 "multiple CEL selectors are ANDed",
			initialDeviceObjects: []runtime.Object{eth0, eth1, eno1},
			opts: []Option{
				WithSelectors([]v1alpha1.Selector{
					{CEL: &v1alpha1.CELDeviceSelector{Expression: `interfaceName.startsWith("eth")`}},
					{CEL: &v1alpha1.CELDeviceSelector{Expression: `interfaceName == "eth0"`}},
				}),
			},
			want: []*Device{eth0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc := newDeviceCache(t, tt.initialDeviceObjects)

			got := dc.List(context.Background(), tt.opts...)
			sortDevices(got)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("List() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
