#!/usr/bin/env bash

# Copyright 2026
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

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

E2E_IMAGE="${E2E_IMAGE:-devicenetwork:e2e}"

cleanup() {
    "$SCRIPT_DIR/e2e-qemu-cluster.sh" down
}
trap cleanup EXIT

"$SCRIPT_DIR/e2e-qemu-cluster.sh" up
E2E_IMAGE="$E2E_IMAGE" "$SCRIPT_DIR/e2e-qemu-cluster.sh" deploy

KUBECONFIG=$("$SCRIPT_DIR/e2e-qemu-cluster.sh" kubeconfig) \
go test "$PROJECT_DIR/test/e2e/..." -v -count=1 \
    --e2e.macvlan-node-name=e2e-node \
    --e2e.macvlan-interface-name="${E2E_INTERFACE:-eth1}"
