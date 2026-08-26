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

package e2e_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	v1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const driverName = "devicenetwork.io"

func getDevicesForNetwork(ctx context.Context, g Gomega, networkName string) []resourcev1.Device {
	slices, err := kubeClient.ResourceV1().ResourceSlices().List(ctx, metav1.ListOptions{})
	g.Expect(err).NotTo(HaveOccurred())

	var devices []resourcev1.Device
	for _, slice := range slices.Items {
		if slice.Spec.Driver != driverName {
			continue
		}
		for _, device := range slice.Spec.Devices {
			attr, ok := device.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork)]
			if ok && attr.StringValue != nil && *attr.StringValue == networkName {
				devices = append(devices, device)
			}
		}
	}

	return devices
}

var _ = Describe("DeviceNetwork", func() {
	It("should publish a device in a ResourceSlice for macvlan DeviceNetwork", func(ctx context.Context) {
		deviceNetwork := &v1alpha1.DeviceNetwork{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-test-net"},
			Spec: v1alpha1.DeviceNetworkSpec{
				DeviceSelectors: []v1alpha1.DeviceSelector{
					{
						Name: "target",
						NodeSelector: &v1.NodeSelector{
							NodeSelectorTerms: []v1.NodeSelectorTerm{
								{
									MatchExpressions: []v1.NodeSelectorRequirement{
										{
											Key:      "kubernetes.io/hostname",
											Operator: v1.NodeSelectorOpIn,
											Values:   []string{macvlanNodeName},
										},
									},
								},
							},
						},
						Selectors: []v1alpha1.Selector{
							{
								CEL: &v1alpha1.CELDeviceSelector{
									Expression: fmt.Sprintf(`interfaceName == %q`, macvlanInterfaceName),
								},
							},
						},
					},
				},
				DeviceConfigurations: []v1alpha1.DeviceConfiguration{
					{
						Name:            "macvlan",
						DeviceType:      &[]v1alpha1.DeviceType{v1alpha1.DeviceTypeMacvlan}[0],
						DeviceSelectors: []string{"target"},
					},
				},
				NetworkInterfaceConfiguration: v1alpha1.NetworkInterfaceConfiguration{
					IPAM: []*v1alpha1.IPAM{
						{
							Provider: v1alpha1.IPAMProviderRandom,
							Random:   &v1alpha1.RandomIPAM{CIDR: "10.99.0.0/24"},
						},
					},
				},
			},
		}

		deviceNetwork, err := deviceNetworkClient.DevicenetworkV1alpha1().DeviceNetworks().Create(ctx, deviceNetwork, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the device to appear in a ResourceSlice")
		Eventually(func(g Gomega) {
			devices := getDevicesForNetwork(ctx, g, "e2e-test-net")

			g.Expect(devices).To(HaveLen(1), "expected exactly one device for interface %s", macvlanInterfaceName)

			d := devices[0]

			hostDevAttr := d.Attributes[resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName)]
			g.Expect(hostDevAttr.StringValue).NotTo(BeNil())
			g.Expect(*hostDevAttr.StringValue).To(Equal(macvlanInterfaceName))

			g.Expect(d.Attributes).To(HaveKey(resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind)))
			g.Expect(d.Attributes).To(HaveKey(resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration)))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("deleting the DeviceNetwork")
		err = deviceNetworkClient.DevicenetworkV1alpha1().DeviceNetworks().Delete(ctx, deviceNetwork.Name, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("verifying the device is removed from ResourceSlices")
		Eventually(func(g Gomega) {
			devices := getDevicesForNetwork(ctx, g, "e2e-test-net")
			g.Expect(devices).To(BeEmpty(), "expected no devices for deleted DeviceNetwork")
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())
	})
})
