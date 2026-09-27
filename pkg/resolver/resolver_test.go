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

package resolver_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	devicenetworkfake "github.com/lioneljouin/devicenetwork/pkg/client/clientset/versioned/fake"
	devicenetworkinformers "github.com/lioneljouin/devicenetwork/pkg/client/informers/externalversions"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	"github.com/lioneljouin/devicenetwork/pkg/resolver"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/ptr"
)

func newResolver(
	ctx context.Context,
	t *testing.T,
	podNetworkKind string,
	initialResourceSlices []runtime.Object,
	initialDeviceNetworks []runtime.Object,
	initialDeviceObjects []runtime.Object,
) *resolver.Resolver {
	t.Helper()

	fakeKubeClient := fake.NewSimpleClientset(initialResourceSlices...)
	fakeDeviceNetworkClient := devicenetworkfake.NewSimpleClientset(initialDeviceNetworks...)

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(fakeKubeClient, 0)
	deviceNetworkInformerFactory := devicenetworkinformers.NewSharedInformerFactory(fakeDeviceNetworkClient, 0)

	deviceCache, err := host.NewDeviceCache(time.Hour)
	if err != nil {
		t.Fatalf("failed to create device cache: %v", err)
	}
	for _, d := range initialDeviceObjects {
		if err := deviceCache.Informer().GetStore().Add(d); err != nil {
			t.Fatalf("failed to add device to cache: %v", d)
		}
	}

	r, err := resolver.NewResolver(
		podNetworkKind,
		kubeInformerFactory.Resource().V1().ResourceSlices(),
		deviceNetworkInformerFactory.Devicenetwork().V1alpha1().DeviceNetworks(),
		deviceCache,
	)
	if err != nil {
		t.Fatal("NewResolver failed:", err)
	}

	kubeInformerFactory.Start(ctx.Done())
	deviceNetworkInformerFactory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(),
		kubeInformerFactory.Resource().V1().ResourceSlices().Informer().HasSynced,
		deviceNetworkInformerFactory.Devicenetwork().V1alpha1().DeviceNetworks().Informer().HasSynced,
	) {
		t.Fatal("failed to wait for caches to sync")
	}

	return r
}

func TestGetDevice(t *testing.T) {
	makeAttrs := func(podNetwork, podNetworkKind, deviceConfig, hostDeviceName string) map[resourcev1.QualifiedName]resourcev1.DeviceAttribute {
		return map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
			resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: ptr.To(podNetwork)},
			resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetworkKind):      {StringValue: ptr.To(podNetworkKind)},
			resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: ptr.To(deviceConfig)},
			resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: ptr.To(hostDeviceName)},
		}
	}

	exposedDevice := resourcev1.Device{
		Name:       "dev-0",
		Attributes: makeAttrs("test-dn", "DeviceNetwork", "config-0", "eth0"),
	}

	deviceNetwork := &v1alpha1.DeviceNetwork{
		ObjectMeta: metav1.ObjectMeta{Name: "test-dn"},
		Spec:       v1alpha1.DeviceNetworkSpec{DeviceConfigurations: []v1alpha1.DeviceConfiguration{{Name: "config-0"}}},
	}

	hostDevice := &host.Device{
		ObjectMeta: metav1.ObjectMeta{Name: "eth0"},
		Spec:       host.DeviceSpec{InterfaceName: "eth0"},
	}

	tests := []struct {
		name                  string
		podNetworkKind        string
		initialResourceSlices []runtime.Object
		initialDeviceNetworks []runtime.Object
		initialDeviceObjects  []runtime.Object
		allocationResult      *resourcev1.DeviceRequestAllocationResult
		want                  *resolver.Device
		wantErr               bool
	}{
		{
			name:           "single device resolved",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver:  "test-driver",
						Pool:    resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{exposedDevice},
					},
				},
			},
			initialDeviceNetworks: []runtime.Object{deviceNetwork},
			initialDeviceObjects:  []runtime.Object{hostDevice},
			allocationResult:      &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
			want: &resolver.Device{
				DeviceRequestAllocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
				DeviceNetwork:                 deviceNetwork,
				DeviceConfiguration:           &deviceNetwork.Spec.DeviceConfigurations[0],
				ExposedDevice:                 &exposedDevice,
				HostDevice:                    hostDevice,
			},
		},
		{
			name:           "device with share ID resolved",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver:  "test-driver",
						Pool:    resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{exposedDevice},
					},
				},
			},
			initialDeviceNetworks: []runtime.Object{deviceNetwork},
			initialDeviceObjects:  []runtime.Object{hostDevice},
			allocationResult: &resourcev1.DeviceRequestAllocationResult{
				Driver:  "test-driver",
				Pool:    "test-pool",
				Device:  "dev-0",
				ShareID: ptr.To(types.UID("share-1")),
			},
			want: &resolver.Device{
				DeviceRequestAllocationResult: &resourcev1.DeviceRequestAllocationResult{
					Driver:  "test-driver",
					Pool:    "test-pool",
					Device:  "dev-0",
					ShareID: ptr.To(types.UID("share-1")),
				},
				DeviceNetwork:       deviceNetwork,
				DeviceConfiguration: &deviceNetwork.Spec.DeviceConfigurations[0],
				ExposedDevice:       &exposedDevice,
				HostDevice:          hostDevice,
			},
		},
		{
			name:             "device not found in any resource slice returns error",
			podNetworkKind:   "DeviceNetwork",
			allocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "missing"},
			wantErr:          true,
		},
		{
			name:           "missing pod network attribute returns error",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver: "test-driver",
						Pool:   resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{
							{
								Name: "dev-0",
								Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetworkKind):      {StringValue: ptr.To("DeviceNetwork")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: ptr.To("config-0")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: ptr.To("eth0")},
								},
							},
						},
					},
				},
			},
			allocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
			wantErr:          true,
		},
		{
			name:           "wrong network kind returns error",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver: "test-driver",
						Pool:   resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{
							{
								Name: "dev-0",
								Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: ptr.To("test-dn")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetworkKind):      {StringValue: ptr.To("WrongKind")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: ptr.To("config-0")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: ptr.To("eth0")},
								},
							},
						},
					},
				},
			},
			allocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
			wantErr:          true,
		},
		{
			name:           "missing device configuration attribute returns error",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver: "test-driver",
						Pool:   resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{
							{
								Name: "dev-0",
								Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):     {StringValue: ptr.To("test-dn")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetworkKind): {StringValue: ptr.To("DeviceNetwork")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName): {StringValue: ptr.To("eth0")},
								},
							},
						},
					},
				},
			},
			allocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
			wantErr:          true,
		},
		{
			name:           "missing host device name attribute returns error",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver: "test-driver",
						Pool:   resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{
							{
								Name: "dev-0",
								Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: ptr.To("test-dn")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetworkKind):      {StringValue: ptr.To("DeviceNetwork")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: ptr.To("config-0")},
								},
							},
						},
					},
				},
			},
			allocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
			wantErr:          true,
		},
		{
			name:           "host device not in cache returns error",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver:  "test-driver",
						Pool:    resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{exposedDevice},
					},
				},
			},
			initialDeviceNetworks: []runtime.Object{deviceNetwork},
			allocationResult:      &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
			wantErr:               true,
		},
		{
			name:           "device network not found returns error",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver:  "test-driver",
						Pool:    resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{exposedDevice},
					},
				},
			},
			initialDeviceObjects: []runtime.Object{hostDevice},
			allocationResult:     &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
			wantErr:              true,
		},
		{
			name:           "device resolved from slice with multiple devices",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver: "test-driver",
						Pool:   resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{
							{Name: "dev-0", Attributes: makeAttrs("test-dn", "DeviceNetwork", "config-0", "eth0")},
							{Name: "dev-1", Attributes: makeAttrs("test-dn", "DeviceNetwork", "config-0", "eth1")},
						},
					},
				},
			},
			initialDeviceNetworks: []runtime.Object{deviceNetwork},
			initialDeviceObjects: []runtime.Object{
				hostDevice,
				&host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth1"}, Spec: host.DeviceSpec{InterfaceName: "eth1"}},
			},
			allocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-1"},
			want: &resolver.Device{
				DeviceRequestAllocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-1"},
				DeviceNetwork:                 deviceNetwork,
				DeviceConfiguration:           &deviceNetwork.Spec.DeviceConfigurations[0],
				ExposedDevice:                 &resourcev1.Device{Name: "dev-1", Attributes: makeAttrs("test-dn", "DeviceNetwork", "config-0", "eth1")},
				HostDevice:                    &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth1"}, Spec: host.DeviceSpec{InterfaceName: "eth1"}},
			},
		},
		{
			name:           "non-network device returns error",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver: "test-driver",
						Pool:   resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{
							{Name: "dev-gpu", Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
								"gpu.vendor": {StringValue: ptr.To("nvidia")},
							}},
						},
					},
				},
			},
			initialDeviceNetworks: []runtime.Object{deviceNetwork},
			initialDeviceObjects:  []runtime.Object{hostDevice},
			allocationResult:      &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-gpu"},
			wantErr:               true,
		},
		{
			name:           "devices from different device networks",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver: "test-driver",
						Pool:   resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{
							{Name: "dev-0", Attributes: makeAttrs("dn-a", "DeviceNetwork", "cfg-a", "eth0")},
							{Name: "dev-1", Attributes: makeAttrs("dn-b", "DeviceNetwork", "cfg-b", "eth1")},
						},
					},
				},
			},
			initialDeviceNetworks: []runtime.Object{
				&v1alpha1.DeviceNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "dn-a"},
					Spec:       v1alpha1.DeviceNetworkSpec{DeviceConfigurations: []v1alpha1.DeviceConfiguration{{Name: "cfg-a"}}},
				},
				&v1alpha1.DeviceNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "dn-b"},
					Spec:       v1alpha1.DeviceNetworkSpec{DeviceConfigurations: []v1alpha1.DeviceConfiguration{{Name: "cfg-b"}}},
				},
			},
			initialDeviceObjects: []runtime.Object{
				hostDevice,
				&host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth1"}, Spec: host.DeviceSpec{InterfaceName: "eth1"}},
			},
			allocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-1"},
			want: &resolver.Device{
				DeviceRequestAllocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-1"},
				DeviceNetwork:                 &v1alpha1.DeviceNetwork{ObjectMeta: metav1.ObjectMeta{Name: "dn-b"}, Spec: v1alpha1.DeviceNetworkSpec{DeviceConfigurations: []v1alpha1.DeviceConfiguration{{Name: "cfg-b"}}}},
				DeviceConfiguration:           &v1alpha1.DeviceConfiguration{Name: "cfg-b"},
				ExposedDevice:                 &resourcev1.Device{Name: "dev-1", Attributes: makeAttrs("dn-b", "DeviceNetwork", "cfg-b", "eth1")},
				HostDevice:                    &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth1"}, Spec: host.DeviceSpec{InterfaceName: "eth1"}},
			},
		},
		{
			name:           "device configuration not found in device network returns error",
			podNetworkKind: "DeviceNetwork",
			initialResourceSlices: []runtime.Object{
				&resourcev1.ResourceSlice{
					ObjectMeta: metav1.ObjectMeta{Name: "slice-0"},
					Spec: resourcev1.ResourceSliceSpec{
						Driver:  "test-driver",
						Pool:    resourcev1.ResourcePool{Name: "test-pool"},
						Devices: []resourcev1.Device{exposedDevice},
					},
				},
			},
			initialDeviceNetworks: []runtime.Object{
				&v1alpha1.DeviceNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "test-dn"},
					Spec:       v1alpha1.DeviceNetworkSpec{DeviceConfigurations: []v1alpha1.DeviceConfiguration{{Name: "other-config"}}},
				},
			},
			initialDeviceObjects: []runtime.Object{hostDevice},
			allocationResult:     &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
			wantErr:              true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			r := newResolver(
				ctx,
				t,
				tt.podNetworkKind,
				tt.initialResourceSlices,
				tt.initialDeviceNetworks,
				tt.initialDeviceObjects,
			)

			got, err := r.GetDevice(tt.allocationResult)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetDevice() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GetDevice() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
