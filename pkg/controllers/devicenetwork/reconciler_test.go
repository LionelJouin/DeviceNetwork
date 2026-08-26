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

package devicenetwork_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	deviceNetworkListers "github.com/lioneljouin/devicenetwork/pkg/client/listers/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/controllers/devicenetwork"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/dynamic-resource-allocation/resourceslice"
)

// sliceSignature builds an order-independent, pointer-independent key for a
// resourceslice.Slice so that Slices can be compared without caring which
// order the reconciler happened to append them in.
func sliceSignature(s resourceslice.Slice) string {
	deviceNames := make([]string, len(s.Devices))
	for i, d := range s.Devices {
		deviceNames[i] = d.Name
	}
	sort.Strings(deviceNames)

	counterNames := make([]string, len(s.SharedCounters))
	for i, c := range s.SharedCounters {
		counterNames[i] = c.Name
	}
	sort.Strings(counterNames)

	return "devices:" + strings.Join(deviceNames, ",") + "|counters:" + strings.Join(counterNames, ",")
}

func newNodeLister(nodes ...*corev1.Node) corev1listers.NodeLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, n := range nodes {
		_ = indexer.Add(n)
	}
	return corev1listers.NewNodeLister(indexer)
}

func newDeviceNetworkLister(networks ...*v1alpha1.DeviceNetwork) deviceNetworkListers.DeviceNetworkLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, n := range networks {
		_ = indexer.Add(n)
	}
	return deviceNetworkListers.NewDeviceNetworkLister(indexer)
}

func newDeviceCache(t *testing.T, devices ...*host.Device) *host.DeviceCache {
	t.Helper()
	dc, err := host.NewDeviceCache(time.Hour)
	if err != nil {
		t.Fatalf("failed to create device cache: %v", err)
	}
	for _, d := range devices {
		if err := dc.Informer().GetStore().Add(d); err != nil {
			t.Fatalf("failed to add device to cache: %v", err)
		}
	}
	return dc
}

type fakeConfigurator struct {
	device *resourcev1.Device
	err    error
}

func (f *fakeConfigurator) ExposedDevice(_ context.Context, _ *host.Device, _ *resourcev1.Device) (*resourcev1.Device, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.device != nil {
		return f.device.DeepCopy(), nil
	}
	return &resourcev1.Device{
		Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{},
	}, nil
}

func (f *fakeConfigurator) Allocate(_ context.Context, _ *host.Device, _ *v1alpha1.DeviceConfiguration, _ *v1alpha1.NetworkInterfaceConfiguration, ads *resourcev1.AllocatedDeviceStatus) (*resourcev1.AllocatedDeviceStatus, error) {
	return ads, nil
}

func (f *fakeConfigurator) Configure(_ context.Context, _ string, ads *resourcev1.AllocatedDeviceStatus) (*resourcev1.AllocatedDeviceStatus, error) {
	return ads, nil
}

func (f *fakeConfigurator) Release(_ context.Context, _ string, _ *resourcev1.AllocatedDeviceStatus) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}

func (f *fakeConfigurator) IsSupported(_ context.Context, _ *host.Device, _ *v1alpha1.DeviceConfiguration) (bool, error) {
	return true, nil
}

var _ configurators.Configurator = (*fakeConfigurator)(nil)

func TestDeviceNetworkReconciler_Reconcile(t *testing.T) {
	macvlanType := v1alpha1.DeviceTypeMacvlan
	networkKind := "DeviceNetwork"

	tests := []struct {
		name                 string
		nodeName             string
		networkKind          string
		nodeLister           corev1listers.NodeLister
		deviceNetworkLister  deviceNetworkListers.DeviceNetworkLister
		publishResourcesFunc devicenetwork.PublishResources
		deviceCache          *host.DeviceCache
		deviceConfigurators  map[v1alpha1.DeviceType]configurators.Configurator
		wantErr              bool
		wantResources        *resourceslice.DriverResources
	}{
		{
			name:                "node not found",
			nodeName:            "missing-node",
			networkKind:         networkKind,
			nodeLister:          newNodeLister(),
			deviceNetworkLister: newDeviceNetworkLister(),
			deviceCache:         newDeviceCache(t),
			deviceConfigurators: map[v1alpha1.DeviceType]configurators.Configurator{},
			wantErr:             true,
		},
		{
			name:        "no device networks",
			nodeName:    "node-a",
			networkKind: networkKind,
			nodeLister: newNodeLister(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			}),
			deviceNetworkLister: newDeviceNetworkLister(),
			publishResourcesFunc: func(_ context.Context, dr resourceslice.DriverResources) error {
				return nil
			},
			deviceCache:         newDeviceCache(t),
			deviceConfigurators: map[v1alpha1.DeviceType]configurators.Configurator{},
			wantErr:             false,
		},
		{
			name:        "nil publishResourcesFunc succeeds",
			nodeName:    "node-a",
			networkKind: networkKind,
			nodeLister: newNodeLister(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			}),
			deviceNetworkLister:  newDeviceNetworkLister(),
			publishResourcesFunc: nil,
			deviceCache:          newDeviceCache(t),
			deviceConfigurators:  map[v1alpha1.DeviceType]configurators.Configurator{},
			wantErr:              false,
		},
		{
			name:        "publishResourcesFunc error",
			nodeName:    "node-a",
			networkKind: networkKind,
			nodeLister: newNodeLister(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			}),
			deviceNetworkLister: newDeviceNetworkLister(),
			publishResourcesFunc: func(_ context.Context, _ resourceslice.DriverResources) error {
				return fmt.Errorf("publish failed")
			},
			deviceCache:         newDeviceCache(t),
			deviceConfigurators: map[v1alpha1.DeviceType]configurators.Configurator{},
			wantErr:             true,
		},
		{
			name:        "device network with matching device",
			nodeName:    "node-a",
			networkKind: networkKind,
			nodeLister: newNodeLister(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			}),
			deviceNetworkLister: newDeviceNetworkLister(&v1alpha1.DeviceNetwork{
				ObjectMeta: metav1.ObjectMeta{Name: "net1"},
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "all-devices",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "macvlan-cfg",
							DeviceSelectors: []string{"all-devices"},
							DeviceType:      &macvlanType,
						},
					},
				},
			}),
			publishResourcesFunc: func(_ context.Context, dr resourceslice.DriverResources) error {
				return nil
			},
			deviceCache: newDeviceCache(t, &host.Device{
				ObjectMeta: metav1.ObjectMeta{Name: "eth0"},
				Spec:       host.DeviceSpec{InterfaceName: "eth0", InterfaceIndex: 2},
			}),
			deviceConfigurators: map[v1alpha1.DeviceType]configurators.Configurator{
				v1alpha1.DeviceTypeMacvlan: &fakeConfigurator{},
			},
			wantErr: false,
			wantResources: func() *resourceslice.DriverResources {
				deviceType := string(v1alpha1.DeviceTypeMacvlan)
				podNetwork := "net1"
				deviceCfg := "macvlan-cfg"
				hostDevName := "eth0"
				return &resourceslice.DriverResources{
					Pools: map[string]resourceslice.Pool{
						"node-a": {Slices: []resourceslice.Slice{
							{
								Devices: []resourcev1.Device{{
									Name: "net1-macvlan-cfg-eth0",
									Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
										resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceType):          {StringValue: &deviceType},
										resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: &podNetwork},
										resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):         {StringValue: &networkKind},
										resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: &deviceCfg},
										resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: &hostDevName},
									},
								}},
							},
							{
								SharedCounters: []resourcev1.CounterSet{{
									Name:     hostDevName,
									Counters: map[string]resourcev1.Counter{"mutual-exclusion": {Value: resource.MustParse("65536")}},
								}},
							},
						}},
					},
				}
			}(),
		},
		{
			name:        "no configurator for device type",
			nodeName:    "node-a",
			networkKind: networkKind,
			nodeLister: newNodeLister(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			}),
			deviceNetworkLister: newDeviceNetworkLister(&v1alpha1.DeviceNetwork{
				ObjectMeta: metav1.ObjectMeta{Name: "net1"},
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{Name: "sel"},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "cfg",
							DeviceSelectors: []string{"sel"},
							DeviceType:      &macvlanType,
						},
					},
				},
			}),
			publishResourcesFunc: func(_ context.Context, dr resourceslice.DriverResources) error {
				return nil
			},
			deviceCache: newDeviceCache(t, &host.Device{
				ObjectMeta: metav1.ObjectMeta{Name: "eth0"},
				Spec:       host.DeviceSpec{InterfaceName: "eth0"},
			}),
			deviceConfigurators: map[v1alpha1.DeviceType]configurators.Configurator{},
			wantErr:             false,
			wantResources: &resourceslice.DriverResources{
				Pools: map[string]resourceslice.Pool{
					"node-a": {Slices: []resourceslice.Slice{
						{
							Devices: []resourcev1.Device{},
						},
						{
							SharedCounters: []resourcev1.CounterSet{},
						},
					}},
				},
			},
		},
		{
			name:        "node selector does not match",
			nodeName:    "node-a",
			networkKind: networkKind,
			nodeLister: newNodeLister(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: map[string]string{"zone": "us-east"},
				},
			}),
			deviceNetworkLister: newDeviceNetworkLister(&v1alpha1.DeviceNetwork{
				ObjectMeta: metav1.ObjectMeta{Name: "net1"},
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "sel",
							NodeSelector: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{{
									MatchExpressions: []corev1.NodeSelectorRequirement{{
										Key:      "zone",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"eu-west"},
									}},
								}},
							},
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "cfg",
							DeviceSelectors: []string{"sel"},
							DeviceType:      &macvlanType,
						},
					},
				},
			}),
			publishResourcesFunc: func(_ context.Context, dr resourceslice.DriverResources) error {
				return nil
			},
			deviceCache: newDeviceCache(t, &host.Device{
				ObjectMeta: metav1.ObjectMeta{Name: "eth0"},
				Spec:       host.DeviceSpec{InterfaceName: "eth0"},
			}),
			deviceConfigurators: map[v1alpha1.DeviceType]configurators.Configurator{
				v1alpha1.DeviceTypeMacvlan: &fakeConfigurator{},
			},
			wantErr: false,
			wantResources: &resourceslice.DriverResources{
				Pools: map[string]resourceslice.Pool{
					"node-a": {Slices: []resourceslice.Slice{
						{
							Devices: []resourcev1.Device{},
						},
						{
							SharedCounters: []resourcev1.CounterSet{},
						},
					}},
				},
			},
		},
		{
			// A single device matched by two DeviceSelectors, each referenced by a
			// different DeviceConfiguration, is currently exposed once per
			// DeviceConfiguration.
			name:        "device matched by multiple selectors is configured multiple times",
			nodeName:    "node-a",
			networkKind: networkKind,
			nodeLister: newNodeLister(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			}),
			deviceNetworkLister: newDeviceNetworkLister(&v1alpha1.DeviceNetwork{
				ObjectMeta: metav1.ObjectMeta{Name: "net1"},
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{Name: "sel-a"},
						{Name: "sel-b"},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "cfg-a",
							DeviceSelectors: []string{"sel-a"},
							DeviceType:      &macvlanType,
						},
						{
							Name:            "cfg-b",
							DeviceSelectors: []string{"sel-b"},
							DeviceType:      &macvlanType,
						},
					},
				},
			}),
			publishResourcesFunc: func(_ context.Context, dr resourceslice.DriverResources) error {
				return nil
			},
			deviceCache: newDeviceCache(t, &host.Device{
				ObjectMeta: metav1.ObjectMeta{Name: "eth0"},
				Spec:       host.DeviceSpec{InterfaceName: "eth0", InterfaceIndex: 2},
			}),
			deviceConfigurators: map[v1alpha1.DeviceType]configurators.Configurator{
				v1alpha1.DeviceTypeMacvlan: &fakeConfigurator{},
			},
			wantErr: false,
			wantResources: func() *resourceslice.DriverResources {
				deviceType := string(v1alpha1.DeviceTypeMacvlan)
				podNetwork := "net1"
				hostDevName := "eth0"
				cfgA := "cfg-a"
				cfgB := "cfg-b"
				return &resourceslice.DriverResources{
					Pools: map[string]resourceslice.Pool{
						"node-a": {Slices: []resourceslice.Slice{
							{
								Devices: []resourcev1.Device{
									{
										Name: "net1-cfg-a-eth0",
										Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceType):          {StringValue: &deviceType},
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: &podNetwork},
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):         {StringValue: &networkKind},
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: &cfgA},
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: &hostDevName},
										},
									},
									{
										Name: "net1-cfg-b-eth0",
										Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceType):          {StringValue: &deviceType},
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: &podNetwork},
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):         {StringValue: &networkKind},
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: &cfgB},
											resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: &hostDevName},
										},
									},
								},
							},
							{
								SharedCounters: []resourcev1.CounterSet{{
									Name:     hostDevName,
									Counters: map[string]resourcev1.Counter{"mutual-exclusion": {Value: resource.MustParse("65536")}},
								}},
							},
						}},
					},
				}
			}(),
		},
		{
			// A single device matched by two DeviceSelectors that are both
			// referenced by the same DeviceConfiguration is currently exposed
			// once.
			name:        "device matched by multiple selectors in the same DeviceConfiguration",
			nodeName:    "node-a",
			networkKind: networkKind,
			nodeLister: newNodeLister(&corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			}),
			deviceNetworkLister: newDeviceNetworkLister(&v1alpha1.DeviceNetwork{
				ObjectMeta: metav1.ObjectMeta{Name: "net1"},
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{Name: "sel-a"},
						{Name: "sel-b"},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "cfg",
							DeviceSelectors: []string{"sel-a", "sel-b"},
							DeviceType:      &macvlanType,
						},
					},
				},
			}),
			publishResourcesFunc: func(_ context.Context, dr resourceslice.DriverResources) error {
				return nil
			},
			deviceCache: newDeviceCache(t, &host.Device{
				ObjectMeta: metav1.ObjectMeta{Name: "eth0"},
				Spec:       host.DeviceSpec{InterfaceName: "eth0", InterfaceIndex: 2},
			}),
			deviceConfigurators: map[v1alpha1.DeviceType]configurators.Configurator{
				v1alpha1.DeviceTypeMacvlan: &fakeConfigurator{},
			},
			wantErr: false,
			wantResources: func() *resourceslice.DriverResources {
				deviceType := string(v1alpha1.DeviceTypeMacvlan)
				podNetwork := "net1"
				hostDevName := "eth0"
				cfg := "cfg"
				duplicateDevice := resourcev1.Device{
					Name: "net1-cfg-eth0",
					Attributes: map[resourcev1.QualifiedName]resourcev1.DeviceAttribute{
						resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceType):          {StringValue: &deviceType},
						resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributePodNetwork):          {StringValue: &podNetwork},
						resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeNetworkKind):         {StringValue: &networkKind},
						resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeDeviceConfiguration): {StringValue: &cfg},
						resourcev1.QualifiedName(v1alpha1.NetworkInterfaceAttributeHostDeviceName):      {StringValue: &hostDevName},
					},
				}
				return &resourceslice.DriverResources{
					Pools: map[string]resourceslice.Pool{
						"node-a": {Slices: []resourceslice.Slice{
							{
								Devices: []resourcev1.Device{duplicateDevice},
							},
							{
								SharedCounters: []resourcev1.CounterSet{{
									Name:     hostDevName,
									Counters: map[string]resourcev1.Counter{"mutual-exclusion": {Value: resource.MustParse("65536")}},
								}},
							},
						}},
					},
				}
			}(),
		},
		// todo: test case with high limit of devices to ensure that the reconciler
		//  can handle large numbers of devices without running into performance issues or timeouts.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured *resourceslice.DriverResources
			publish := tt.publishResourcesFunc
			if publish != nil && tt.wantResources != nil {
				orig := publish
				publish = func(ctx context.Context, dr resourceslice.DriverResources) error {
					captured = &dr
					return orig(ctx, dr)
				}
			}

			dnr, err := devicenetwork.NewDeviceNetworkReconciler(tt.nodeName, tt.networkKind, tt.nodeLister, tt.deviceNetworkLister, publish, tt.deviceCache, tt.deviceConfigurators)
			if err != nil {
				t.Fatalf("could not construct receiver type: %v", err)
			}
			gotErr := dnr.Reconcile(t.Context())
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("Reconcile() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("Reconcile() succeeded unexpectedly")
			}

			if tt.wantResources != nil && captured != nil {
				diff := cmp.Diff(*tt.wantResources, *captured,
					cmpopts.EquateEmpty(),
					cmpopts.SortSlices(func(a, b resourceslice.Slice) bool {
						return sliceSignature(a) < sliceSignature(b)
					}),
				)
				if diff != "" {
					t.Errorf("published resources mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
