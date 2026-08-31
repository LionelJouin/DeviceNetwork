#!/usr/bin/env bash

# Copyright 2026 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Run the full e2e test suite using a QEMU-based cluster.
#
# Usage:
#   hack/e2e/e2e-qemu.sh
#
# Prerequisites (Debian/Ubuntu):
#   sudo apt install -y qemu-system-x86 qemu-utils genisoimage curl openssh-client
# Prerequisites (Fedora/RHEL):
#   sudo dnf install -y qemu-system-x86 qemu-img genisoimage curl openssh-clients
# KVM support is also required (/dev/kvm); add yourself to the kvm group if needed:
#   sudo usermod -aG kvm "$USER"  # re-login afterwards

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

E2E_IMAGE="${E2E_IMAGE:-devicenetwork:e2e}"

# check_prerequisites verifies the required host tools are installed and prints
# an install hint if any are missing.
check_prerequisites() {
    local missing=()
    command -v qemu-system-x86_64 &>/dev/null || missing+=("qemu-system-x86_64 (qemu-system-x86)")
    command -v qemu-img &>/dev/null || missing+=("qemu-img (qemu-utils)")
    if ! command -v genisoimage &>/dev/null \
        && ! command -v mkisofs &>/dev/null \
        && ! command -v xorriso &>/dev/null; then
        missing+=("genisoimage (genisoimage), or mkisofs/xorriso")
    fi
    command -v curl &>/dev/null || missing+=("curl (curl)")
    command -v ssh &>/dev/null || missing+=("ssh (openssh-client)")

    if [ ${#missing[@]} -ne 0 ]; then
        echo "ERROR: missing required tools:" >&2
        printf '  - %s\n' "${missing[@]}" >&2
        echo >&2
        echo "Install them (Debian/Ubuntu):" >&2
        echo "  sudo apt install -y qemu-system-x86 qemu-utils genisoimage curl openssh-client" >&2
        echo "Install them (Fedora/RHEL):" >&2
        echo "  sudo dnf install -y qemu-system-x86 qemu-img genisoimage curl openssh-clients" >&2
        exit 1
    fi

    if [ ! -e /dev/kvm ]; then
        echo "WARNING: /dev/kvm not found; the VM may fail to start or run very slowly." >&2
        echo "  Enable virtualization and ensure KVM is available (see 'kvm-ok')." >&2
    fi
}

check_prerequisites

cleanup() {
    "$SCRIPT_DIR/e2e-qemu-cluster.sh" down
}
trap cleanup EXIT

"$SCRIPT_DIR/e2e-qemu-cluster.sh" up
E2E_IMAGE="$E2E_IMAGE" "$SCRIPT_DIR/e2e-qemu-cluster.sh" deploy

# Tests that cannot run on the QEMU-based cluster:
#   - "HostDevice RDMA" (test/e2e/hostdevice_rdma_test.go): requires a hardware
#     RDMA NIC where the Ethernet and RDMA functions share a PCI device, so the
#     RDMA device shows up under /sys/class/net/<if>/device/infiniband. QEMU can
#     no longer emulate an RDMA HCA (pvrdma was removed in QEMU 9.1), and the
#     software RDMA drivers (rxe, siw) register virtual devices with no PCI
#     parent, so that sysfs path never exists here.
KUBECONFIG=$("$SCRIPT_DIR/e2e-qemu-cluster.sh" kubeconfig) \
go test "$PROJECT_DIR/test/e2e/..." -v -count=1 -ginkgo.v \
    -ginkgo.skip="HostDevice RDMA" \
    --e2e.macvlan-node-name=e2e-node \
    --e2e.macvlan-interface-name="${E2E_INTERFACE:-eth1}" \
    --e2e.hostdevice-node-name=e2e-node \
    --e2e.hostdevice-interface-name="${E2E_HOSTDEVICE_INTERFACE:-eth2}"
