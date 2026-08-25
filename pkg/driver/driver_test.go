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

package driver

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	"github.com/lioneljouin/devicenetwork/pkg/resolver"
	"github.com/lioneljouin/devicenetwork/pkg/store"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/utils/ptr"
)

type fakeDeviceResolver struct {
	devices []*resolver.Device
	err     error
}

func (f *fakeDeviceResolver) GetDevices(_ string, _ *resourcev1.ResourceClaim) ([]*resolver.Device, error) {
	return f.devices, f.err
}

type fakeConfigurator struct {
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus
	err                   error
}

func (f *fakeConfigurator) ExposedDevice(_ context.Context, _ *host.Device, _ *resourcev1.Device) (*resourcev1.Device, error) {
	return nil, nil
}

func (f *fakeConfigurator) Allocate(_ context.Context, _ *host.Device, _ *v1alpha1.DeviceConfiguration, _ *v1alpha1.NetworkInterfaceConfiguration, _ *resourcev1.AllocatedDeviceStatus) (*resourcev1.AllocatedDeviceStatus, error) {
	return f.allocatedDeviceStatus, f.err
}

func (f *fakeConfigurator) Configure(_ context.Context, _ string, _ *resourcev1.AllocatedDeviceStatus) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}

func (f *fakeConfigurator) Release(_ context.Context, _ string, _ *resourcev1.AllocatedDeviceStatus) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}

func newTestDriver(
	t *testing.T,
	kubeClient kubernetes.Interface,
	deviceResolver deviceResolver,
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator,
) *Driver {
	t.Helper()

	return &Driver{
		driverName:          "test-driver",
		kubeClient:          kubeClient,
		podResourceStore:    store.NewMemory(),
		deviceResolver:      deviceResolver,
		deviceConfigurators: deviceConfigurators,
	}
}

func TestDriver_PrepareResourceClaims(t *testing.T) {
	deviceNetwork := &v1alpha1.DeviceNetwork{
		ObjectMeta: metav1.ObjectMeta{Name: "net1"},
		Spec: v1alpha1.DeviceNetworkSpec{
			DeviceConfigurations: []v1alpha1.DeviceConfiguration{
				{Name: "macvlan"},
			},
		},
	}

	hostDev := &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth0"}, Spec: host.DeviceSpec{InterfaceName: "eth0"}}

	makeClaim := func(uid types.UID, reservations int, results []resourcev1.DeviceRequestAllocationResult) *resourcev1.ResourceClaim {
		claim := &resourcev1.ResourceClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-claim",
				Namespace: "default",
				UID:       uid,
			},
			Status: resourcev1.ResourceClaimStatus{
				Allocation: &resourcev1.AllocationResult{
					Devices: resourcev1.DeviceAllocationResult{
						Results: results,
					},
				},
			},
		}
		for i := range reservations {
			claim.Status.ReservedFor = append(claim.Status.ReservedFor, resourcev1.ResourceClaimConsumerReference{
				UID: types.UID(fmt.Sprintf("pod-uid-%d", i)),
			})
		}
		return claim
	}

	result0 := resourcev1.DeviceRequestAllocationResult{
		Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth0", Request: "req-0",
	}

	allocatedStatus := &resourcev1.AllocatedDeviceStatus{
		Driver:      "test-driver",
		Pool:        "node-1",
		Device:      "net1-macvlan-eth0",
		NetworkData: &resourcev1.NetworkDeviceData{InterfaceName: "net1"},
	}

	resolvedDevice := func() *resolver.Device {
		return &resolver.Device{
			DeviceRequestAllocationResult: &result0,
			DeviceNetwork:                 deviceNetwork,
			DeviceConfiguration:           &deviceNetwork.Spec.DeviceConfigurations[0],
			HostDevice:                    hostDev,
		}
	}

	defaultConfigurators := map[v1alpha1.DeviceType]configurators.Configurator{
		v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{allocatedDeviceStatus: allocatedStatus},
	}

	tests := []struct {
		name      string
		driver    *Driver
		claims    []*resourcev1.ResourceClaim
		want      map[types.UID]kubeletplugin.PrepareResult
		wantErr   bool
		wantClErr bool // per-claim error expected
	}{
		{
			name: "allocate claim with resolved device",
			driver: newTestDriver(t,
				fake.NewClientset(makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})),
				&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
				defaultConfigurators,
			),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name:      "no reservations returns per-claim error",
			driver:    newTestDriver(t, fake.NewClientset(), &fakeDeviceResolver{}, nil),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 0, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name:      "multiple reservations returns per-claim error",
			driver:    newTestDriver(t, fake.NewClientset(), &fakeDeviceResolver{}, nil),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 2, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "resolver error returns per-claim error",
			driver: newTestDriver(t,
				fake.NewClientset(),
				&fakeDeviceResolver{err: fmt.Errorf("resolver failure")},
				nil,
			),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "other driver results are filtered out",
			driver: newTestDriver(t,
				fake.NewClientset(makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{
					result0,
					{Driver: "other-driver", Pool: "node-1", Device: "gpu-0", Request: "req-1"},
				})),
				&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
				defaultConfigurators,
			),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{
				result0,
				{Driver: "other-driver", Pool: "node-1", Device: "gpu-0", Request: "req-1"},
			})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name: "multiple results for this driver",
			driver: func() *Driver {
				r1 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth0", Request: "req-0"}
				r2 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth1", Request: "req-1"}
				as1 := &resourcev1.AllocatedDeviceStatus{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth0"}
				as2 := &resourcev1.AllocatedDeviceStatus{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth1"}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r1, r2})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{
						// first device already allocated — skipped by allocateDevices
						{DeviceRequestAllocationResult: &r1, AllocatedDeviceStatus: as1, DeviceNetwork: deviceNetwork, DeviceConfiguration: &deviceNetwork.Spec.DeviceConfigurations[0], HostDevice: hostDev},
						{DeviceRequestAllocationResult: &r2, DeviceNetwork: deviceNetwork, DeviceConfiguration: &deviceNetwork.Spec.DeviceConfigurations[0], HostDevice: &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth1"}}},
					}},
					map[v1alpha1.DeviceType]configurators.Configurator{
						v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{allocatedDeviceStatus: as2},
					},
				)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				r1 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth0", Request: "req-0"}
				r2 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth1", Request: "req-1"}
				return []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r1, r2})}
			}(),
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{
					{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"},
					{Requests: []string{"req-1"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth1"},
				}},
			},
		},
		{
			name: "already allocated device is skipped by allocateDevices",
			driver: func() *Driver {
				dev := resolvedDevice()
				dev.AllocatedDeviceStatus = allocatedStatus
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					nil,
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name: "nil device configuration is skipped",
			driver: func() *Driver {
				dev := resolvedDevice()
				dev.DeviceConfiguration = nil
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					defaultConfigurators,
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name: "no configurator for device type is skipped",
			driver: func() *Driver {
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
					map[v1alpha1.DeviceType]configurators.Configurator{},
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name: "configurator allocate error skips device",
			driver: func() *Driver {
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
					map[v1alpha1.DeviceType]configurators.Configurator{
						v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{err: fmt.Errorf("allocate failure")},
					},
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name: "allocated status with share ID",
			driver: func() *Driver {
				shareID := types.UID("share-1")
				r := result0
				r.ShareID = &shareID
				as := &resourcev1.AllocatedDeviceStatus{
					Driver:  "test-driver",
					Pool:    "node-1",
					Device:  "net1-macvlan-eth0",
					ShareID: ptr.To("share-1"),
				}
				dev := &resolver.Device{
					DeviceRequestAllocationResult: &r,
					DeviceNetwork:                 deviceNetwork,
					DeviceConfiguration:           &deviceNetwork.Spec.DeviceConfigurations[0],
					HostDevice:                    hostDev,
				}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					map[v1alpha1.DeviceType]configurators.Configurator{
						v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{allocatedDeviceStatus: as},
					},
				)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				shareID := types.UID("share-1")
				r := result0
				r.ShareID = &shareID
				return []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r})}
			}(),
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name: "allocated status with conditions",
			driver: func() *Driver {
				as := &resourcev1.AllocatedDeviceStatus{
					Driver: "test-driver",
					Pool:   "node-1",
					Device: "net1-macvlan-eth0",
					Conditions: []metav1.Condition{
						{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Allocated"},
					},
				}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
					map[v1alpha1.DeviceType]configurators.Configurator{
						v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{allocatedDeviceStatus: as},
					},
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name: "allocated status with data",
			driver: func() *Driver {
				as := &resourcev1.AllocatedDeviceStatus{
					Driver: "test-driver",
					Pool:   "node-1",
					Device: "net1-macvlan-eth0",
					Data:   &runtime.RawExtension{Raw: []byte(`{"key":"value"}`)},
				}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
					map[v1alpha1.DeviceType]configurators.Configurator{
						v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{allocatedDeviceStatus: as},
					},
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "net1-macvlan-eth0"}}},
			},
		},
		{
			name: "multiple claims processed independently",
			driver: func() *Driver {
				r1 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "dev-0", Request: "req-0"}
				r2 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "dev-1", Request: "req-1"}
				c1 := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r1})
				c2 := makeClaim("c2", 1, []resourcev1.DeviceRequestAllocationResult{r2})
				c2.Name = "test-claim-2"
				c2.UID = "c2"
				as := &resourcev1.AllocatedDeviceStatus{Driver: "test-driver", Pool: "node-1", Device: "dev-1"}
				return newTestDriver(t,
					fake.NewClientset(c1, c2),
					&fakeDeviceResolver{devices: []*resolver.Device{
						{DeviceRequestAllocationResult: &r1, DeviceNetwork: deviceNetwork, DeviceConfiguration: &deviceNetwork.Spec.DeviceConfigurations[0], HostDevice: hostDev},
					}},
					map[v1alpha1.DeviceType]configurators.Configurator{
						v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{allocatedDeviceStatus: as},
					},
				)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				r1 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "dev-0", Request: "req-0"}
				r2 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "dev-1", Request: "req-1"}
				c1 := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r1})
				c2 := makeClaim("c2", 1, []resourcev1.DeviceRequestAllocationResult{r2})
				c2.Name = "test-claim-2"
				c2.UID = "c2"
				return []*resourcev1.ResourceClaim{c1, c2}
			}(),
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{Requests: []string{"req-0"}, PoolName: "node-1", DeviceName: "dev-0"}}},
				"c2": {Devices: []kubeletplugin.Device{{Requests: []string{"req-1"}, PoolName: "node-1", DeviceName: "dev-1"}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := tt.driver.PrepareResourceClaims(t.Context(), tt.claims)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("PrepareResourceClaims() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("PrepareResourceClaims() succeeded unexpectedly")
			}

			for uid, result := range got {
				if tt.wantClErr {
					if result.Err == nil {
						t.Errorf("expected per-claim error for UID %s, got nil", uid)
					}
					continue
				}
				if result.Err != nil {
					t.Errorf("unexpected per-claim error for UID %s: %v", uid, result.Err)
					continue
				}
				if diff := cmp.Diff(tt.want[uid], result); diff != "" {
					t.Errorf("PrepareResourceClaims() mismatch for UID %s (-want +got):\n%s", uid, diff)
				}
			}
		})
	}
}
