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

package device_test

import (
	"context"
	"slices"
	"testing"

	"github.com/lioneljouin/devicenetwork/pkg/device"
)

func TestEvalDevices(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		devices    []*device.Device
		want       []*device.Device
		wantErr    bool
	}{
		{
			name:       "valid-expression-no-device",
			expression: "interfaceName == 'eth0'",
			devices:    []*device.Device{},
			want:       []*device.Device{},
			wantErr:    false,
		},
		{
			name:       "valid-expression-two-devices-one-match",
			expression: "interfaceName == 'eth0'",
			devices: []*device.Device{
				{Spec: device.DeviceSpec{InterfaceName: "eth0"}},
				{Spec: device.DeviceSpec{InterfaceName: "eth1"}},
			},
			want: []*device.Device{
				{Spec: device.DeviceSpec{InterfaceName: "eth0"}},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotErr := device.EvalDevices(context.Background(), tt.expression, tt.devices)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("EvalDevices() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("EvalDevices() succeeded unexpectedly")
			}
			if !slices.EqualFunc(got, tt.want, func(a, b *device.Device) bool {
				return a.Spec.InterfaceName == b.Spec.InterfaceName
			}) {
				t.Errorf("EvalDevices() = %v, want %v", got, tt.want)
			}
		})
	}
}
