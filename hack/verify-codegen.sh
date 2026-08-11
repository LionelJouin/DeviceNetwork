#!/bin/bash

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

set -o errexit
set -o nounset
set -o pipefail

cd "$(git rev-parse --show-toplevel)" || exit 1

DIFFROOT="$(pwd)"
TMP_DIFFROOT="$(mktemp -d)"

cleanup() {
    rm -rf "${TMP_DIFFROOT}"
}
trap cleanup EXIT

cp -a "${DIFFROOT}"/{apis,pkg,deployment} "${TMP_DIFFROOT}/"

hack/update-codegen.sh

if diff -Naupr "${TMP_DIFFROOT}/apis" "${DIFFROOT}/apis" && \
   diff -Naupr "${TMP_DIFFROOT}/pkg/client" "${DIFFROOT}/pkg/client" && \
   diff -Naupr "${TMP_DIFFROOT}/deployment" "${DIFFROOT}/deployment"; then
    echo "codegen is up to date."
else
    echo "error: codegen is out of date. Please run hack/update-codegen.sh" >&2
    exit 1
fi
