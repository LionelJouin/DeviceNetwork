/*
Copyright 2026

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
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func newResolver(
	ctx context.Context,
	t *testing.T,
	networkKind string,
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
		networkKind,
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

func strPtr(s string) *string { return &s }

func TestGetDevices(t *testing.T) {
	deviceAttrs := map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
		resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: strPtr("test-dn")},
		resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):         {StringValue: strPtr("DeviceNetwork")},
		resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: strPtr("config-0")},
		resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: strPtr("eth0")},
	}

	exposedDevice := resourcev1.Device{
		Name:       "dev-0",
		Attributes: deviceAttrs,
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
		networkKind           string
		initialResourceSlices []runtime.Object
		initialDeviceNetworks []runtime.Object
		initialDeviceObjects  []runtime.Object
		driverName            string
		claim                 *resourcev1.ResourceClaim
		want                  []*resolver.Device
		wantErr               bool
	}{
		{
			name:        "single device resolved",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
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
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			want: []*resolver.Device{
				{
					DeviceRequestAllocationResult: &resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"},
					DeviceNetwork:                 deviceNetwork,
					DeviceConfiguration:           &deviceNetwork.Spec.DeviceConfigurations[0],
					ExposedDevice:                 &exposedDevice,
					HostDevice:                    hostDevice,
				},
			},
		},
		{
			name:        "driver name does not match skips device",
			networkKind: "DeviceNetwork",
			driverName:  "other-driver",
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
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			want: nil,
		},
		{
			name:        "device not found in any resource slice",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "missing"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name:        "missing pod network attribute",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
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
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):         {StringValue: strPtr("DeviceNetwork")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: strPtr("config-0")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: strPtr("eth0")},
								},
							},
						},
					},
				},
			},
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name:        "wrong network kind",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
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
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: strPtr("test-dn")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):         {StringValue: strPtr("WrongKind")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: strPtr("config-0")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: strPtr("eth0")},
								},
							},
						},
					},
				},
			},
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name:        "missing device configuration attribute",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
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
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):     {StringValue: strPtr("test-dn")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):    {StringValue: strPtr("DeviceNetwork")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName): {StringValue: strPtr("eth0")},
								},
							},
						},
					},
				},
			},
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name:        "missing host device name attribute",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
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
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: strPtr("test-dn")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):         {StringValue: strPtr("DeviceNetwork")},
									resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: strPtr("config-0")},
								},
							},
						},
					},
				},
			},
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name:        "host device not in cache",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
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
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name:        "device network not found",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
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
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name:        "device configuration not found in device network",
			networkKind: "DeviceNetwork",
			driverName:  "test-driver",
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
			claim: &resourcev1.ResourceClaim{
				Status: resourcev1.ResourceClaimStatus{
					Allocation: &resourcev1.AllocationResult{
						Devices: resourcev1.DeviceAllocationResult{
							Results: []resourcev1.DeviceRequestAllocationResult{{Driver: "test-driver", Pool: "test-pool", Device: "dev-0"}},
						},
					},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			r := newResolver(
				ctx,
				t,
				tt.networkKind,
				tt.initialResourceSlices,
				tt.initialDeviceNetworks,
				tt.initialDeviceObjects,
			)

			got, err := r.GetDevices(tt.driverName, tt.claim)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetDevices() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GetDevices() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
