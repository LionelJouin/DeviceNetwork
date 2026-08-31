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

package host_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lioneljouin/devicenetwork/pkg/host"
)

func TestRDMADevicesForNetdev(t *testing.T) {
	mkdir := func(elem ...string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(elem...), 0o755); err != nil {
			t.Fatalf("failed to create sysfs dir: %v", err)
		}
	}

	// emptyRoot: the interface has no sysfs entry at all.
	emptyRoot := t.TempDir()

	// noRDMARoot: the interface exists but has no device/infiniband path.
	noRDMARoot := t.TempDir()
	mkdir(noRDMARoot, "eth0")

	// singleRoot: the interface is backed by one RDMA device.
	singleRoot := t.TempDir()
	mkdir(singleRoot, "eth0", "device", "infiniband", "mlx5_0")

	// multiRoot: the interface is backed by multiple RDMA devices. os.ReadDir
	// returns entries sorted by name, so the result order is deterministic.
	multiRoot := t.TempDir()
	mkdir(multiRoot, "eth0", "device", "infiniband", "mlx5_0")
	mkdir(multiRoot, "eth0", "device", "infiniband", "mlx5_1")

	// fileRoot: device/infiniband is a file instead of a directory, so reading
	// it fails with an error other than "not exist".
	fileRoot := t.TempDir()
	mkdir(fileRoot, "eth0", "device")
	if err := os.WriteFile(filepath.Join(fileRoot, "eth0", "device", "infiniband"), nil, 0o644); err != nil {
		t.Fatalf("failed to create infiniband file: %v", err)
	}

	tests := []struct {
		name         string
		sysfsNetRoot string
		ifName       string
		want         []string
		wantErr      bool
	}{
		{
			name:         "missing interface returns no devices",
			sysfsNetRoot: emptyRoot,
			ifName:       "eth0",
			want:         nil,
		},
		{
			name:         "interface without an RDMA device returns no devices",
			sysfsNetRoot: noRDMARoot,
			ifName:       "eth0",
			want:         nil,
		},
		{
			name:         "single RDMA device",
			sysfsNetRoot: singleRoot,
			ifName:       "eth0",
			want:         []string{"mlx5_0"},
		},
		{
			name:         "multiple RDMA devices",
			sysfsNetRoot: multiRoot,
			ifName:       "eth0",
			want:         []string{"mlx5_0", "mlx5_1"},
		},
		{
			name:         "infiniband path is not a directory",
			sysfsNetRoot: fileRoot,
			ifName:       "eth0",
			wantErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := host.RDMADevicesForNetdev(tt.sysfsNetRoot, tt.ifName)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("RDMADevicesForNetdev() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("RDMADevicesForNetdev() succeeded unexpectedly")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RDMADevicesForNetdev() = %v, want %v", got, tt.want)
			}
		})
	}
}
