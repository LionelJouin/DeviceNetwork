/*
Copyright (c) 2026

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

package e2e_test

import (
	"flag"
	"testing"

	devicenetworkclientset "github.com/lioneljouin/devicenetwork/pkg/client/clientset/versioned"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	kubeClient          kubernetes.Interface
	deviceNetworkClient devicenetworkclientset.Interface

	macvlanNodeName      string
	macvlanInterfaceName string
)

func init() {
	flag.StringVar(&macvlanNodeName, "e2e.macvlan-node-name", "", "Node with the interface to create macvlans on (required for macvlan tests)")
	flag.StringVar(&macvlanInterfaceName, "e2e.macvlan-interface-name", "", "Host interface name to create macvlans on (required for macvlan tests)")
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite")
}

var _ = BeforeSuite(func() {
	Expect(macvlanNodeName).NotTo(BeEmpty(), "--e2e.macvlan-node-name is required")
	Expect(macvlanInterfaceName).NotTo(BeEmpty(), "--e2e.macvlan-interface-name is required")

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	cfg, err := kubeConfig.ClientConfig()
	Expect(err).NotTo(HaveOccurred(), "failed to load kubeconfig")

	kubeClient, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())

	deviceNetworkClient, err = devicenetworkclientset.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
})
