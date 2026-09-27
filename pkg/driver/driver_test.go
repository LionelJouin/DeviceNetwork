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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	multinetworkv1alpha1 "github.com/kubernetes-sigs/multi-network-api/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	"github.com/lioneljouin/devicenetwork/pkg/resolver"
	"github.com/lioneljouin/devicenetwork/pkg/status"
	"github.com/lioneljouin/devicenetwork/pkg/store"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/utils/ptr"
)

type fakeDeviceResolver struct {
	devices []*resolver.Device
	err     error
}

func (f *fakeDeviceResolver) GetDevice(result *resourcev1.DeviceRequestAllocationResult) (*resolver.Device, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, dev := range f.devices {
		if dev == nil {
			return nil, nil
		}
		if dev.DeviceRequestAllocationResult == nil {
			return dev, nil
		}
		if dev.DeviceRequestAllocationResult.Driver == result.Driver &&
			dev.DeviceRequestAllocationResult.Pool == result.Pool &&
			dev.DeviceRequestAllocationResult.Device == result.Device {
			return dev, nil
		}
	}
	return nil, fmt.Errorf("device not found: %s/%s/%s", result.Driver, result.Pool, result.Device)
}

func (f *fakeDeviceResolver) GetDevices(_ string, _ *resourcev1.ResourceClaim) ([]*resolver.Device, error) {
	return f.devices, f.err
}

type allocateCall struct {
	hostDevice                    *host.Device
	deviceConfiguration           *v1alpha1.DeviceConfiguration
	networkInterfaceConfiguration *v1alpha1.NetworkInterfaceConfiguration
	allocatedDeviceStatus         *resourcev1.AllocatedDeviceStatus
}

type fakeConfigurator struct {
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus
	err                   error
	allocateCalls         []allocateCall
}

func (f *fakeConfigurator) ExposedDevice(_ context.Context, _ *host.Device, _ *resourcev1.Device) (*resourcev1.Device, error) {
	return nil, nil
}

func (f *fakeConfigurator) Allocate(
	_ context.Context,
	hd *host.Device,
	dc *v1alpha1.DeviceConfiguration,
	nic *v1alpha1.NetworkInterfaceConfiguration,
	in *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	f.allocateCalls = append(f.allocateCalls, allocateCall{
		hostDevice:                    hd,
		deviceConfiguration:           dc,
		networkInterfaceConfiguration: nic,
		allocatedDeviceStatus:         in,
	})
	if f.err != nil {
		return nil, f.err
	}
	if f.allocatedDeviceStatus != nil {
		res := in.DeepCopy()
		if f.allocatedDeviceStatus.NetworkData != nil {
			res.NetworkData = f.allocatedDeviceStatus.NetworkData.DeepCopy()
		}
		return res, nil
	}
	return in, nil
}

func (f *fakeConfigurator) Configure(_ context.Context, _ string, _ *resourcev1.AllocatedDeviceStatus) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}

func (f *fakeConfigurator) Release(_ context.Context, _ string, _ *resourcev1.AllocatedDeviceStatus) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}

func (f *fakeConfigurator) IsSupported(_ context.Context, _ *host.Device, _ *v1alpha1.DeviceConfiguration) (bool, error) {
	return true, nil
}

func newTestDriver(
	t *testing.T,
	kubeClient kubernetes.Interface,
	deviceResolver deviceResolver,
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator,
) *Driver {
	t.Helper()

	if deviceConfigurators == nil {
		deviceConfigurators = map[v1alpha1.DeviceType]configurators.Configurator{
			v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{},
			v1alpha1.DeviceTypeMacvlan:    &fakeConfigurator{},
		}
	}

	return &Driver{
		podNetworkKind:      "test-pod-network",
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

	resolvedDevice := func() *resolver.Device {
		return &resolver.Device{
			DeviceRequestAllocationResult: &result0,
			DeviceNetwork:                 deviceNetwork,
			DeviceConfiguration:           &deviceNetwork.Spec.DeviceConfigurations[0],
			HostDevice:                    hostDev,
			ExposedDevice:                 &resourcev1.Device{Name: "net1-macvlan-eth0"},
		}
	}

	makeDevice := func(request, pool, name string, networkData *resourcev1.NetworkDeviceData, shareID *types.UID) kubeletplugin.Device {
		if networkData == nil {
			networkData = &resourcev1.NetworkDeviceData{}
		}
		dev := kubeletplugin.Device{
			Requests:   []string{request},
			PoolName:   pool,
			DeviceName: name,
			Metadata: &kubeletplugin.DeviceMetadata{
				Attributes:  map[string]resourcev1.DeviceAttribute{},
				NetworkData: networkData,
			},
		}
		if shareID != nil {
			dev.ShareID = shareID
		}
		return dev
	}

	makeStatusDevice := func(driver, pool, device string, shareID *string, netName string, hostDev *host.Device, devConfig *v1alpha1.DeviceConfiguration) resourcev1.AllocatedDeviceStatus {
		statusData := &status.ResourceClaimDeviceStatusData{
			PodNetwork: &multinetworkv1alpha1.PodNetwork{
				Kind: "test-pod-network",
				Name: netName,
			},
			Device:              hostDev.DeepCopy(),
			DeviceConfiguration: devConfig.DeepCopy(),
		}
		raw, _ := json.Marshal(statusData)
		return resourcev1.AllocatedDeviceStatus{
			Driver:      driver,
			Pool:        pool,
			Device:      device,
			ShareID:     shareID,
			NetworkData: &resourcev1.NetworkDeviceData{},
			Data:        &runtime.RawExtension{Raw: raw},
			Conditions: []metav1.Condition{
				{
					Type:   status.DeviceStatusConditionAllocation,
					Status: metav1.ConditionTrue,
					Reason: status.DeviceStatusReasonAllocated,
				},
			},
		}
	}

	defaultConfigurators := map[v1alpha1.DeviceType]configurators.Configurator{
		v1alpha1.DeviceTypeHostDevice: &fakeConfigurator{},
		v1alpha1.DeviceTypeMacvlan:    &fakeConfigurator{},
	}

	tests := []struct {
		name              string
		driver            *Driver
		claims            []*resourcev1.ResourceClaim
		want              map[types.UID]kubeletplugin.PrepareResult
		wantStatusDevices map[types.UID][]resourcev1.AllocatedDeviceStatus
		wantErr           bool
		wantClErr         bool // per-claim error expected
		verify            func(t *testing.T, d *Driver)
	}{
		{
			name: "allocate claim with resolved device",
			driver: func() *Driver {
				conf := &fakeConfigurator{}
				return newTestDriver(t,
					fake.NewClientset(makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})),
					&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
					map[v1alpha1.DeviceType]configurators.Configurator{
						v1alpha1.DeviceTypeHostDevice: conf,
					},
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{makeDevice("req-0", "node-1", "net1-macvlan-eth0", nil, nil)}},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": {makeStatusDevice("test-driver", "node-1", "net1-macvlan-eth0", nil, "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0])},
			},
			verify: func(t *testing.T, d *Driver) {
				conf := d.deviceConfigurators[v1alpha1.DeviceTypeHostDevice].(*fakeConfigurator)
				if len(conf.allocateCalls) != 1 {
					t.Fatalf("expected 1 Allocate call on configurator, got %d", len(conf.allocateCalls))
				}
				call := conf.allocateCalls[0]
				if call.hostDevice != hostDev {
					t.Errorf("unexpected hostDevice in Allocate call: %+v", call.hostDevice)
				}
				if call.deviceConfiguration != &deviceNetwork.Spec.DeviceConfigurations[0] {
					t.Errorf("unexpected deviceConfiguration in Allocate call: %+v", call.deviceConfiguration)
				}
				if call.networkInterfaceConfiguration != &deviceNetwork.Spec.NetworkInterfaceConfiguration {
					t.Errorf("unexpected networkInterfaceConfiguration in Allocate call: %+v", call.networkInterfaceConfiguration)
				}
				if call.allocatedDeviceStatus.Device != "net1-macvlan-eth0" || call.allocatedDeviceStatus.Driver != "test-driver" {
					t.Errorf("unexpected allocatedDeviceStatus in Allocate call: %+v", call.allocatedDeviceStatus)
				}
				if call.allocatedDeviceStatus.Data == nil || len(call.allocatedDeviceStatus.Data.Raw) == 0 {
					t.Errorf("expected allocatedDeviceStatus.Data to be populated before calling Allocate")
				}
			},
		},
		{
			name: "configurator is called and updates network data on allocated device",
			driver: func() *Driver {
				conf := &fakeConfigurator{
					allocatedDeviceStatus: &resourcev1.AllocatedDeviceStatus{
						NetworkData: &resourcev1.NetworkDeviceData{
							InterfaceName:   "custom-net0",
							IPs:             []string{"192.168.1.10/24"},
							HardwareAddress: "02:00:00:00:00:01",
						},
					},
				}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
					map[v1alpha1.DeviceType]configurators.Configurator{
						v1alpha1.DeviceTypeHostDevice: conf,
					},
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{
					makeDevice("req-0", "node-1", "net1-macvlan-eth0", &resourcev1.NetworkDeviceData{
						InterfaceName:   "custom-net0",
						IPs:             []string{"192.168.1.10/24"},
						HardwareAddress: "02:00:00:00:00:01",
					}, nil),
				}},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": {func() resourcev1.AllocatedDeviceStatus {
					s := makeStatusDevice("test-driver", "node-1", "net1-macvlan-eth0", nil, "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0])
					s.NetworkData = &resourcev1.NetworkDeviceData{
						InterfaceName:   "custom-net0",
						IPs:             []string{"192.168.1.10/24"},
						HardwareAddress: "02:00:00:00:00:01",
					}
					return s
				}()},
			},
			verify: func(t *testing.T, d *Driver) {
				conf := d.deviceConfigurators[v1alpha1.DeviceTypeHostDevice].(*fakeConfigurator)
				if len(conf.allocateCalls) != 1 {
					t.Fatalf("expected 1 Allocate call on configurator, got %d", len(conf.allocateCalls))
				}
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
			name: "claim status allocation is nil returns per-claim error",
			driver: func() *Driver {
				claim := makeClaim("c1", 1, nil)
				claim.Status.Allocation = nil
				return newTestDriver(t, fake.NewClientset(claim), &fakeDeviceResolver{}, nil)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				claim := makeClaim("c1", 1, nil)
				claim.Status.Allocation = nil
				return []*resourcev1.ResourceClaim{claim}
			}(),
			wantClErr: true,
		},
		{
			name: "API ApplyStatus failure returns per-claim error",
			driver: func() *Driver {
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				client := fake.NewClientset(claim)
				client.PrependReactor("patch", "resourceclaims", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, fmt.Errorf("api apply status failure")
				})
				return newTestDriver(t, client, &fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}}, nil)
			}(),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "claim with no matching driver results succeeds with empty devices",
			driver: func() *Driver {
				r := resourcev1.DeviceRequestAllocationResult{Driver: "other-driver", Pool: "node-1", Device: "gpu-0", Request: "req-1"}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r})
				return newTestDriver(t, fake.NewClientset(claim), &fakeDeviceResolver{}, nil)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				r := resourcev1.DeviceRequestAllocationResult{Driver: "other-driver", Pool: "node-1", Device: "gpu-0", Request: "req-1"}
				return []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r})}
			}(),
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: nil},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": nil,
			},
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
				"c1": {Devices: []kubeletplugin.Device{makeDevice("req-0", "node-1", "net1-macvlan-eth0", nil, nil)}},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": {makeStatusDevice("test-driver", "node-1", "net1-macvlan-eth0", nil, "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0])},
			},
		},
		{
			name: "multiple results for this driver",
			driver: func() *Driver {
				r1 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth0", Request: "req-0"}
				r2 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth1", Request: "req-1"}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r1, r2})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{
						{DeviceRequestAllocationResult: &r1, DeviceNetwork: deviceNetwork, DeviceConfiguration: &deviceNetwork.Spec.DeviceConfigurations[0], HostDevice: hostDev, ExposedDevice: &resourcev1.Device{Name: "net1-macvlan-eth0"}},
						{DeviceRequestAllocationResult: &r2, DeviceNetwork: deviceNetwork, DeviceConfiguration: &deviceNetwork.Spec.DeviceConfigurations[0], HostDevice: &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth1"}}, ExposedDevice: &resourcev1.Device{Name: "net1-macvlan-eth1"}},
					}},
					nil,
				)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				r1 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth0", Request: "req-0"}
				r2 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth1", Request: "req-1"}
				return []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r1, r2})}
			}(),
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{
					makeDevice("req-0", "node-1", "net1-macvlan-eth0", nil, nil),
					makeDevice("req-1", "node-1", "net1-macvlan-eth1", nil, nil),
				}},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": {
					makeStatusDevice("test-driver", "node-1", "net1-macvlan-eth0", nil, "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0]),
					makeStatusDevice("test-driver", "node-1", "net1-macvlan-eth1", nil, "net1", &host.Device{ObjectMeta: metav1.ObjectMeta{Name: "eth1"}}, &deviceNetwork.Spec.DeviceConfigurations[0]),
				},
			},
		},
		{
			name: "already allocated device in claim status is re-allocated",
			driver: func() *Driver {
				dev := resolvedDevice()
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				claim.Status.Devices = []resourcev1.AllocatedDeviceStatus{
					{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth0"},
				}
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					nil,
				)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				claim.Status.Devices = []resourcev1.AllocatedDeviceStatus{
					{Driver: "test-driver", Pool: "node-1", Device: "net1-macvlan-eth0"},
				}
				return []*resourcev1.ResourceClaim{claim}
			}(),
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{
					makeDevice("req-0", "node-1", "net1-macvlan-eth0", nil, nil),
				}},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": {makeStatusDevice("test-driver", "node-1", "net1-macvlan-eth0", nil, "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0])},
			},
		},
		{
			name: "nil device configuration returns per-claim error",
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
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "no configurator found for device type returns per-claim error",
			driver: func() *Driver {
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{resolvedDevice()}},
					map[v1alpha1.DeviceType]configurators.Configurator{},
				)
			}(),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "configurator allocate error returns per-claim error",
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
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "nil resolved device returns per-claim error",
			driver: func() *Driver {
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{nil}},
					nil,
				)
			}(),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "nil device request allocation result returns per-claim error",
			driver: func() *Driver {
				dev := resolvedDevice()
				dev.DeviceRequestAllocationResult = nil
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					nil,
				)
			}(),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "nil exposed device returns per-claim error",
			driver: func() *Driver {
				dev := resolvedDevice()
				dev.ExposedDevice = nil
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					nil,
				)
			}(),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "nil device network returns per-claim error",
			driver: func() *Driver {
				dev := resolvedDevice()
				dev.DeviceNetwork = nil
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					nil,
				)
			}(),
			claims:    []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			wantClErr: true,
		},
		{
			name: "allocated status with share ID",
			driver: func() *Driver {
				shareID := types.UID("share-1")
				r := result0
				r.ShareID = &shareID
				dev := &resolver.Device{
					DeviceRequestAllocationResult: &r,
					DeviceNetwork:                 deviceNetwork,
					DeviceConfiguration:           &deviceNetwork.Spec.DeviceConfigurations[0],
					HostDevice:                    hostDev,
					ExposedDevice:                 &resourcev1.Device{Name: "net1-macvlan-eth0"},
				}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					nil,
				)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				shareID := types.UID("share-1")
				r := result0
				r.ShareID = &shareID
				return []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r})}
			}(),
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{
					makeDevice("req-0", "node-1", "net1-macvlan-eth0", nil, ptr.To(types.UID("share-1"))),
				}},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": {makeStatusDevice("test-driver", "node-1", "net1-macvlan-eth0", ptr.To("share-1"), "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0])},
			},
		},
		{
			name: "exposed device attributes are copied to device metadata",
			driver: func() *Driver {
				dev := resolvedDevice()
				dev.ExposedDevice = &resourcev1.Device{
					Name: "net1-macvlan-eth0",
					Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
						"pciAddress": {StringValue: ptr.To("0000:00:01.0")},
					},
				}
				claim := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})
				return newTestDriver(t,
					fake.NewClientset(claim),
					&fakeDeviceResolver{devices: []*resolver.Device{dev}},
					nil,
				)
			}(),
			claims: []*resourcev1.ResourceClaim{makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{result0})},
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{{
					Requests:   []string{"req-0"},
					PoolName:   "node-1",
					DeviceName: "net1-macvlan-eth0",
					Metadata: &kubeletplugin.DeviceMetadata{
						Attributes: map[string]resourcev1.DeviceAttribute{
							"pciAddress": {StringValue: ptr.To("0000:00:01.0")},
						},
						NetworkData: &resourcev1.NetworkDeviceData{},
					},
				}}},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": {makeStatusDevice("test-driver", "node-1", "net1-macvlan-eth0", nil, "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0])},
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
				c2.Status.ReservedFor = []resourcev1.ResourceClaimConsumerReference{{UID: "pod-c2"}}
				return newTestDriver(t,
					fake.NewClientset(c1, c2),
					&fakeDeviceResolver{devices: []*resolver.Device{
						{DeviceRequestAllocationResult: &r1, DeviceNetwork: deviceNetwork, DeviceConfiguration: &deviceNetwork.Spec.DeviceConfigurations[0], HostDevice: hostDev, ExposedDevice: &resourcev1.Device{Name: "dev-0"}},
						{DeviceRequestAllocationResult: &r2, DeviceNetwork: deviceNetwork, DeviceConfiguration: &deviceNetwork.Spec.DeviceConfigurations[0], HostDevice: hostDev, ExposedDevice: &resourcev1.Device{Name: "dev-1"}},
					}},
					nil,
				)
			}(),
			claims: func() []*resourcev1.ResourceClaim {
				r1 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "dev-0", Request: "req-0"}
				r2 := resourcev1.DeviceRequestAllocationResult{Driver: "test-driver", Pool: "node-1", Device: "dev-1", Request: "req-1"}
				c1 := makeClaim("c1", 1, []resourcev1.DeviceRequestAllocationResult{r1})
				c2 := makeClaim("c2", 1, []resourcev1.DeviceRequestAllocationResult{r2})
				c2.Name = "test-claim-2"
				c2.UID = "c2"
				c2.Status.ReservedFor = []resourcev1.ResourceClaimConsumerReference{{UID: "pod-c2"}}
				return []*resourcev1.ResourceClaim{c1, c2}
			}(),
			want: map[types.UID]kubeletplugin.PrepareResult{
				"c1": {Devices: []kubeletplugin.Device{makeDevice("req-0", "node-1", "dev-0", nil, nil)}},
				"c2": {Devices: []kubeletplugin.Device{makeDevice("req-1", "node-1", "dev-1", nil, nil)}},
			},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{
				"c1": {makeStatusDevice("test-driver", "node-1", "dev-0", nil, "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0])},
				"c2": {makeStatusDevice("test-driver", "node-1", "dev-1", nil, "net1", hostDev, &deviceNetwork.Spec.DeviceConfigurations[0])},
			},
		},
		{
			name:              "empty claims list succeeds",
			driver:            newTestDriver(t, fake.NewClientset(), &fakeDeviceResolver{}, nil),
			claims:            []*resourcev1.ResourceClaim{},
			want:              map[types.UID]kubeletplugin.PrepareResult{},
			wantStatusDevices: map[types.UID][]resourcev1.AllocatedDeviceStatus{},
		},
	}

	transformRawExtension := cmp.Transformer("RawExtensionJSON", func(in *runtime.RawExtension) map[string]any {
		if in == nil || len(in.Raw) == 0 {
			return nil
		}
		var out map[string]any
		_ = json.Unmarshal(in.Raw, &out)
		return out
	})

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

			for _, claim := range tt.claims {
				result, ok := got[claim.UID]
				if !ok {
					t.Errorf("missing result for UID %s", claim.UID)
					continue
				}
				if tt.wantClErr {
					if result.Err == nil {
						t.Errorf("expected per-claim error for UID %s, got nil", claim.UID)
					}
					for _, res := range claim.Status.ReservedFor {
						if stored := tt.driver.podResourceStore.(*store.Memory).Get(res.UID); len(stored) > 0 {
							t.Errorf("expected no claims stored for pod %s on error, got %d", res.UID, len(stored))
						}
					}
					continue
				}
				if result.Err != nil {
					t.Errorf("unexpected per-claim error for UID %s: %v", claim.UID, result.Err)
					continue
				}
				if diff := cmp.Diff(tt.want[claim.UID], result); diff != "" {
					t.Errorf("PrepareResourceClaims() mismatch for UID %s (-want +got):\n%s", claim.UID, diff)
				}

				// 1. Verify the claim status was updated in Kubernetes API
				k8sClaim, err := tt.driver.kubeClient.ResourceV1().ResourceClaims(claim.Namespace).Get(t.Context(), claim.Name, metav1.GetOptions{})
				if err != nil {
					t.Errorf("failed to get claim %s/%s from Kubernetes API: %v", claim.Namespace, claim.Name, err)
				} else {
					if diff := cmp.Diff(tt.wantStatusDevices[claim.UID], k8sClaim.Status.Devices, cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"), cmpopts.EquateEmpty(), transformRawExtension); diff != "" {
						t.Errorf("Kubernetes API Status.Devices mismatch for UID %s (-want +got):\n%s", claim.UID, diff)
					}
				}

				// 2. Verify the claim stored in podResourceStore matches the expected status
				for _, res := range claim.Status.ReservedFor {
					stored := tt.driver.podResourceStore.(*store.Memory).Get(res.UID)
					if len(stored) == 0 {
						t.Errorf("expected claim to be stored in podResourceStore for pod %s, but found none", res.UID)
					} else {
						if diff := cmp.Diff(tt.wantStatusDevices[claim.UID], stored[0].Status.Devices, cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime"), cmpopts.EquateEmpty(), transformRawExtension); diff != "" {
							t.Errorf("podResourceStore Status.Devices mismatch for UID %s (-want +got):\n%s", claim.UID, diff)
						}
					}
				}
			}

			if tt.verify != nil {
				tt.verify(t, tt.driver)
			}
		})
	}
}
