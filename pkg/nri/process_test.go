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

package nri

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	"github.com/lioneljouin/devicenetwork/pkg/status"
	resourcev1 "k8s.io/api/resource/v1"
	kruntime "k8s.io/apimachinery/pkg/runtime"
)

// fakeConfigurator is a test double for configurators.Configurator that records
// how many times Configure was called, the arguments of the last call, and can
// be made to return a configured error.
type fakeConfigurator struct {
	configureCalls int
	configureErr   error
	gotNamespace   string
	gotStatus      *resourcev1.AllocatedDeviceStatus
}

func (f *fakeConfigurator) ExposedDevice(
	_ context.Context,
	_ *host.Device,
	_ *resourcev1.Device,
) (*resourcev1.Device, error) {
	return nil, nil
}

func (f *fakeConfigurator) Allocate(
	_ context.Context,
	_ *host.Device,
	_ *v1alpha1.DeviceConfiguration,
	_ *v1alpha1.NetworkInterfaceConfiguration,
	_ *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}

func (f *fakeConfigurator) Configure(
	_ context.Context,
	podNetworkNamespace string,
	allocatedDeviceStatus *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	f.configureCalls++
	f.gotNamespace = podNetworkNamespace
	f.gotStatus = allocatedDeviceStatus

	if f.configureErr != nil {
		return nil, f.configureErr
	}

	return allocatedDeviceStatus, nil
}

func (f *fakeConfigurator) Release(
	_ context.Context,
	_ string,
	_ *resourcev1.AllocatedDeviceStatus,
) (*resourcev1.AllocatedDeviceStatus, error) {
	return nil, nil
}

// deviceStatusClaim returns a ResourceClaim with a single device status for the
// given driver whose data is the given raw JSON payload.
func deviceStatusClaim(driver string, raw []byte) *resourcev1.ResourceClaim {
	return &resourcev1.ResourceClaim{
		Status: resourcev1.ResourceClaimStatus{
			Devices: []resourcev1.AllocatedDeviceStatus{
				{
					Driver: driver,
					Data:   &kruntime.RawExtension{Raw: raw},
				},
			},
		},
	}
}

// claimWithDeviceType returns a ResourceClaim with a single device status for
// the given driver selecting the given device type. A nil deviceType
// simulates a status whose device type was never set.
func claimWithDeviceType(driver string, deviceType *v1alpha1.DeviceType) *resourcev1.ResourceClaim {
	raw, _ := json.Marshal(&status.ResourceClaimDeviceStatusData{
		DeviceConfiguration: &v1alpha1.DeviceConfiguration{DeviceType: deviceType},
	})

	return deviceStatusClaim(driver, raw)
}

// macvlanClaim returns a ResourceClaim with a single device status for the given
// driver whose data selects the Macvlan device type.
func macvlanClaim(driver string) *resourcev1.ResourceClaim {
	deviceType := v1alpha1.DeviceTypeMacvlan

	return claimWithDeviceType(driver, &deviceType)
}

func Test_configurationProcess(t *testing.T) {
	const (
		driverName          = "device.network"
		podNetworkNamespace = "/proc/1/ns/net"
	)

	hostDeviceType := v1alpha1.DeviceTypeHostDevice

	tests := []struct {
		name               string
		claims             []*resourcev1.ResourceClaim
		configureErr       error
		wantConfigureCalls int
	}{
		{
			name:               "configures a device matching the driver",
			claims:             []*resourcev1.ResourceClaim{macvlanClaim(driverName)},
			wantConfigureCalls: 1,
		},
		{
			name:               "skips a device belonging to another driver",
			claims:             []*resourcev1.ResourceClaim{macvlanClaim("other.driver")},
			wantConfigureCalls: 0,
		},
		{
			name:               "skips a device type with no registered configurator",
			claims:             []*resourcev1.ResourceClaim{claimWithDeviceType(driverName, &hostDeviceType)},
			wantConfigureCalls: 0,
		},
		{
			name:               "skips a device status with no device type set",
			claims:             []*resourcev1.ResourceClaim{claimWithDeviceType(driverName, nil)},
			wantConfigureCalls: 0,
		},
		{
			name:               "skips a device status with malformed data",
			claims:             []*resourcev1.ResourceClaim{deviceStatusClaim(driverName, []byte("not json"))},
			wantConfigureCalls: 0,
		},
		{
			name:               "still completes when Configure fails",
			claims:             []*resourcev1.ResourceClaim{macvlanClaim(driverName)},
			configureErr:       errors.New("boom"),
			wantConfigureCalls: 1,
		},
		{
			name: "configures every matching device across multiple claims",
			claims: []*resourcev1.ResourceClaim{
				macvlanClaim(driverName),
				macvlanClaim(driverName),
			},
			wantConfigureCalls: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeConfigurator{configureErr: tt.configureErr}
			cp := newConfigurationProcess(
				nil,
				podNetworkNamespace,
				tt.claims,
				driverName,
				map[v1alpha1.DeviceType]configurators.Configurator{
					v1alpha1.DeviceTypeMacvlan: fake,
				},
			)

			// A freshly built process has not started yet, so it reports done.
			if !cp.isDone() {
				t.Fatal("isDone() = false before run(), want true")
			}

			cp.run(t.Context())

			// Once run() returns, the process must report done, even if a
			// device failed to configure or was skipped.
			if !cp.isDone() {
				t.Error("isDone() = false after run(), want true")
			}

			if fake.configureCalls != tt.wantConfigureCalls {
				t.Errorf("Configure() called %d times, want %d", fake.configureCalls, tt.wantConfigureCalls)
			}

			if tt.wantConfigureCalls > 0 && fake.gotNamespace != podNetworkNamespace {
				t.Errorf("Configure() called with namespace %q, want %q", fake.gotNamespace, podNetworkNamespace)
			}
		})
	}
}

// Test_configurationProcess_concurrentAccess mirrors how Plugin uses a
// configurationProcess: run() is started in its own goroutine from
// RunPodSandbox, while isDone() and terminate() are called concurrently from
// the NRI stub's dispatch goroutine (StartContainer, RemovePodSandbox). Run
// with -race to catch unsynchronized access to the done/cancel fields.
func Test_configurationProcess_concurrentAccess(t *testing.T) {
	fake := &fakeConfigurator{}
	cp := newConfigurationProcess(
		nil,
		"/proc/1/ns/net",
		[]*resourcev1.ResourceClaim{
			macvlanClaim("device.network"),
			macvlanClaim("device.network"),
			macvlanClaim("device.network"),
		},
		"device.network",
		map[v1alpha1.DeviceType]configurators.Configurator{
			v1alpha1.DeviceTypeMacvlan: fake,
		},
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		cp.run(t.Context())
	}()

	for !cp.isDone() {
	}

	<-done
	cp.terminate()
}
