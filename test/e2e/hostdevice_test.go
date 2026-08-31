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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	v1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("HostDevice", func() {
	const (
		networkName       = "e2e-hostdevice-net"
		claimTemplateName = "e2e-hostdevice-claim"
		podName           = "e2e-hostdevice-pod"
		containerName     = "alpine"
		namespace         = "default"
	)

	It("moves a host interface into a Pod that claims the device", func(ctx context.Context) {
		By("creating a host-device DeviceNetwork")
		deviceNetwork := &v1alpha1.DeviceNetwork{
			ObjectMeta: metav1.ObjectMeta{Name: networkName},
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
											Values:   []string{hostDeviceNodeName},
										},
									},
								},
							},
						},
						Selectors: []v1alpha1.Selector{
							{
								CEL: &v1alpha1.CELDeviceSelector{
									Expression: fmt.Sprintf(`interfaceName == %q`, hostDeviceInterfaceName),
								},
							},
						},
					},
				},
				DeviceConfigurations: []v1alpha1.DeviceConfiguration{
					{
						Name:            "host-device",
						DeviceType:      &[]v1alpha1.DeviceType{v1alpha1.DeviceTypeHostDevice}[0],
						DeviceSelectors: []string{"target"},
					},
				},
			},
		}

		_, err := deviceNetworkClient.DevicenetworkV1alpha1().DeviceNetworks().Create(ctx, deviceNetwork, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			err := deviceNetworkClient.DevicenetworkV1alpha1().DeviceNetworks().Delete(ctx, networkName, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		By("waiting for the host device to be published in a ResourceSlice")
		Eventually(func(g Gomega) {
			devices := getDevicesForNetwork(ctx, g, networkName)
			g.Expect(devices).To(HaveLen(1), "expected exactly one device for interface %s", hostDeviceInterfaceName)
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("creating a ResourceClaimTemplate that selects the host device")
		claimTemplate := &resourcev1.ResourceClaimTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: claimTemplateName, Namespace: namespace},
			Spec: resourcev1.ResourceClaimTemplateSpec{
				Spec: resourcev1.ResourceClaimSpec{
					Devices: resourcev1.DeviceClaim{
						Requests: []resourcev1.DeviceRequest{
							{
								Name: "host-device",
								Exactly: &resourcev1.ExactDeviceRequest{
									DeviceClassName: "devicenetwork",
									AllocationMode:  resourcev1.DeviceAllocationModeExactCount,
									Count:           1,
									Selectors: []resourcev1.DeviceSelector{
										{
											CEL: &resourcev1.CELDeviceSelector{
												Expression: fmt.Sprintf(
													`device.attributes["multinetwork.networking.k8s.io"].podNetwork == %q && device.attributes["devicenetwork.io"].deviceConfiguration == "host-device"`,
													networkName,
												),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		_, err = kubeClient.ResourceV1().ResourceClaimTemplates(namespace).Create(ctx, claimTemplate, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			err := kubeClient.ResourceV1().ResourceClaimTemplates(namespace).Delete(ctx, claimTemplateName, metav1.DeleteOptions{})
			Expect(err).NotTo(HaveOccurred())
		})

		By("creating a Pod that claims the host device")
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: namespace,
			},
			Spec: v1.PodSpec{
				NodeSelector: map[string]string{"kubernetes.io/hostname": hostDeviceNodeName},
				Tolerations: []v1.Toleration{
					{Operator: v1.TolerationOpExists, Effect: v1.TaintEffectNoSchedule},
					{Operator: v1.TolerationOpExists, Effect: v1.TaintEffectNoExecute},
				},
				Containers: []v1.Container{
					{
						Name:            containerName,
						Image:           "alpine:latest",
						ImagePullPolicy: v1.PullIfNotPresent,
						Command:         []string{"sleep", "infinity"},
					},
				},
				ResourceClaims: []v1.PodResourceClaim{
					{
						Name:                      "host-device",
						ResourceClaimTemplateName: &[]string{claimTemplateName}[0],
					},
				},
			},
		}

		_, err = kubeClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			err := kubeClient.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
			if !apierrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		})

		By("waiting for the Pod to be Running")
		Eventually(func(g Gomega) {
			p, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(p.Status.Phase).To(Equal(v1.PodRunning), "pod phase is %s", p.Status.Phase)
		}).WithTimeout(2 * time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		By("verifying the ResourceClaim reports the moved interface")
		var claimName string
		Eventually(func(g Gomega) {
			p, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(p.Status.ResourceClaimStatuses).NotTo(BeEmpty())

			rcName := p.Status.ResourceClaimStatuses[0].ResourceClaimName
			g.Expect(rcName).NotTo(BeNil(), "pod has no generated ResourceClaim name")

			claim, err := kubeClient.ResourceV1().ResourceClaims(namespace).Get(ctx, *rcName, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())

			var deviceStatus *resourcev1.AllocatedDeviceStatus
			for i := range claim.Status.Devices {
				if claim.Status.Devices[i].Driver == driverName {
					deviceStatus = &claim.Status.Devices[i]
					break
				}
			}
			g.Expect(deviceStatus).NotTo(BeNil(), "no device status for driver %s", driverName)
			g.Expect(deviceStatus.NetworkData).NotTo(BeNil())
			// A host device keeps its original name when moved into the Pod.
			g.Expect(deviceStatus.NetworkData.InterfaceName).To(Equal(hostDeviceInterfaceName))

			claimName = *rcName
		}).WithTimeout(time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		By(fmt.Sprintf("confirming the host interface %q was moved inside the Pod", hostDeviceInterfaceName))
		Eventually(func(g Gomega) {
			stdout, stderr, err := execInPod(ctx, namespace, podName, containerName,
				[]string{"cat", fmt.Sprintf("/sys/class/net/%s/ifindex", hostDeviceInterfaceName)})
			g.Expect(err).NotTo(HaveOccurred(), "exec failed, stderr: %s", stderr)
			g.Expect(strings.TrimSpace(stdout)).NotTo(BeEmpty(), "interface %s has no ifindex in the pod", hostDeviceInterfaceName)
		}).WithTimeout(time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		By("confirming the host device is removed from the node's ResourceSlice while claimed")
		// Once moved into the Pod the interface is no longer on the host, so the
		// driver stops publishing it.
		Eventually(func(g Gomega) {
			devices := getDevicesForNetwork(ctx, g, networkName)
			g.Expect(devices).To(BeEmpty(), "interface %s should not be published while moved into the Pod", hostDeviceInterfaceName)
		}).WithTimeout(time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		By("deleting the Pod")
		err = kubeClient.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			_, err := kubeClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "pod still exists")
		}).WithTimeout(time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		By("verifying the generated ResourceClaim is released")
		Eventually(func(g Gomega) {
			_, err := kubeClient.ResourceV1().ResourceClaims(namespace).Get(ctx, claimName, metav1.GetOptions{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "ResourceClaim %s still exists", claimName)
		}).WithTimeout(time.Minute).WithPolling(2 * time.Second).Should(Succeed())

		By("verifying the host device is returned to the node and re-published")
		Eventually(func(g Gomega) {
			devices := getDevicesForNetwork(ctx, g, networkName)
			g.Expect(devices).To(HaveLen(1), "interface %s should be published again after Pod deletion", hostDeviceInterfaceName)
		}).WithTimeout(time.Minute).WithPolling(2 * time.Second).Should(Succeed())
	})
})
