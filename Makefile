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

REGISTRY ?= localhost:5000/devicenetwork
VERSION ?= $(shell git describe --dirty --tags --always 2>/dev/null)

# Set TEST_AS_ROOT=true to run tests requiring root privileges (network namespace
# and link creation). Some tests also require kernel modules to be loaded for
# full coverage (e.g. rdma_rxe for SoftRoCE, rdma_siw for Software iWARP):
#   sudo modprobe rdma_rxe
#   sudo modprobe rdma_siw
TEST_AS_ROOT ?= false

all: verify test build-image

.PHONY: verify
verify:
	hack/verify-all.sh

.PHONY: test
test:
	@mkdir -p _output
ifeq ($(TEST_AS_ROOT),true)
	sudo env "PATH=$$PATH" go test ./pkg/... ./cmd/... ./apis/... -race -count=1 -coverprofile=_output/coverage.out
else
	@echo "WARNING: TEST_AS_ROOT is not set; tests requiring root privileges will be skipped (set TEST_AS_ROOT=true to run all tests)"
	go test ./pkg/... ./cmd/... ./apis/... -race -count=1 -coverprofile=_output/coverage.out
endif

.PHONY: .build-image
build-image:
	docker build -t devicenetwork:$(VERSION) -f ./build/Dockerfile .

.PHONY: push-image
push-image: build-image
	docker tag devicenetwork:$(VERSION) $(REGISTRY)/devicenetwork:$(VERSION)
	docker push $(REGISTRY)/devicenetwork:$(VERSION)

# --- e2e ---

.PHONY: e2e
e2e:
	E2E_IMAGE=devicenetwork:$(VERSION) hack/e2e/e2e-qemu.sh