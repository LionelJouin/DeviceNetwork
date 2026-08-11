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

REGISTRY ?= localhost:5000/devicenetwork
VERSION ?= $(shell git describe --dirty --tags --always 2>/dev/null)

all: verify build-image

.PHONY: verify
verify:
	hack/verify-all.sh

.PHONY: .build-image
build-image:
	docker build -t devicenetwork:$(VERSION) -f ./build/Dockerfile .

.PHONY: push-image
push-image: build-image
	docker tag devicenetwork:$(VERSION) $(REGISTRY)/devicenetwork:$(VERSION)
	docker push $(REGISTRY)/devicenetwork:$(VERSION)

# --- e2e ---

.PHONY: e2e-build-image
e2e-build-image:
	hack/e2e/e2e-cluster.sh build

.PHONY: e2e-up
e2e-up:
	hack/e2e/e2e-cluster.sh up

.PHONY: e2e-down
e2e-down:
	hack/e2e/e2e-cluster.sh down

.PHONY: e2e-deploy
e2e-deploy:
	hack/e2e/e2e-cluster.sh deploy

.PHONY: e2e-test
e2e-test:
	KUBECONFIG=$$(hack/e2e/e2e-cluster.sh kubeconfig) \
	go test ./test/e2e/... -v -count=1 \
		--e2e.node-name=e2e-node \
		--e2e.interface-name=$${E2E_INTERFACE:-eth1}

.PHONY: e2e
e2e: e2e-up e2e-deploy e2e-test

.PHONY: e2e-clean
e2e-clean:
	hack/e2e/e2e-cluster.sh clean