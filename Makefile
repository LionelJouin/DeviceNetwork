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

all: verify test build-image

.PHONY: verify
verify:
	hack/verify-all.sh

.PHONY: test
test:
	sudo env "PATH=$$PATH" go test ./pkg/... ./cmd/... ./apis/... -race -count=1

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