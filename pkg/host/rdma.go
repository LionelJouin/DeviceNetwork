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
	"fmt"
	"os"
	"path/filepath"
)

// DefaultSysfsNetRoot is the sysfs path holding the network device class in
// production. It is a variable so tests can point discovery at a temporary
// directory instead of the real /sys.
const DefaultSysfsNetRoot = "/sys/class/net"

// RDMADevicesForNetdev returns the names of the RDMA (InfiniBand) devices backed
// by the same PCI device as the network device ifName.
//
// Discovery reads <sysfsNetRoot>/<ifName>/device/infiniband, the InfiniBand
// devices hanging off the netdev's PCI device. This covers hardware RDMA NICs,
// where the Ethernet and RDMA functions share a PCI device.
//
// A netdev without an RDMA device (or without a PCI device at all) returns no
// devices and no error.
func RDMADevicesForNetdev(sysfsNetRoot, ifName string) ([]string, error) {
	ibRoot := filepath.Join(sysfsNetRoot, ifName, "device", "infiniband")

	entries, err := os.ReadDir(ibRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read RDMA devices %q: %v", ibRoot, err)
	}

	var devices []string
	for _, entry := range entries {
		devices = append(devices, entry.Name())
	}

	return devices, nil
}

// isRDMACapable reports whether the interface has at least one associated RDMA
// device, i.e. it is RDMA-capable.
func isRDMACapable(sysfsNetRoot, ifName string) bool {
	devices, err := RDMADevicesForNetdev(sysfsNetRoot, ifName)

	return err == nil && len(devices) > 0
}
