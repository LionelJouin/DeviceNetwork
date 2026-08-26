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
package validation_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/dynamic-resource-allocation/api"
)

func TestValidateDeviceNetwork(t *testing.T) {
	deviceTypeHostDevice := v1alpha1.DeviceTypeHostDevice
	deviceTypeMacvlan := v1alpha1.DeviceTypeMacvlan
	expression := `device.numa == 0`

	tests := []struct {
		name          string
		deviceNetwork *v1alpha1.DeviceNetwork
		want          field.ErrorList
	}{
		{
			name: "valid-minimal",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
					},
				},
			},
			want: field.ErrorList{},
		},
		{
			name: "valid-node-selector",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
							NodeSelector: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{"worker"}}}}},
							},
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
					},
				},
			},
			want: field.ErrorList{},
		},
		{
			name: "valid-selector",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name:      "selector1",
							Selectors: []v1alpha1.Selector{{CEL: &v1alpha1.CELDeviceSelector{Expression: expression}}},
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
					},
				},
			},
			want: field.ErrorList{},
		},
		{
			name: "valid-host-device",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
							DeviceType:      &deviceTypeHostDevice,
						},
					},
				},
			},
			want: field.ErrorList{},
		},
		{
			name: "valid-macvlan",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{

					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
							DeviceType:      &deviceTypeMacvlan,
						},
					},
				},
			},
			want: field.ErrorList{},
		},
		{
			name: "invalid-duplicate-device-selectors-name",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
						{
							Name: "selector1",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Duplicate(field.NewPath("spec").Child("deviceSelectors").Index(1), "selector1"),
			},
		},
		{
			name: "invalid-duplicate-device-configurations-name",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name:      "selector1",
							Selectors: []v1alpha1.Selector{{CEL: &v1alpha1.CELDeviceSelector{Expression: expression}}},
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Duplicate(field.NewPath("spec").Child("deviceConfigurations").Index(1), "config1"),
			},
		},
		{
			name: "invalid-duplicate-device-selectors-in-device-configurations",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name:      "selector1",
							Selectors: []v1alpha1.Selector{{CEL: &v1alpha1.CELDeviceSelector{Expression: expression}}},
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1", "selector1"},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Duplicate(field.NewPath("spec").Child("deviceConfigurations").Index(0).Child("deviceSelectors").Index(1), "selector1"),
			},
		},
		{
			name: "invalid-unknown-device-selectors-in-device-configurations",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name:      "selector1",
							Selectors: []v1alpha1.Selector{{CEL: &v1alpha1.CELDeviceSelector{Expression: expression}}},
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1", "unknownSelector"},
						},
					},
				},
			},
			want: field.ErrorList{
				field.NotFound(field.NewPath("spec").Child("deviceConfigurations").Index(0).Child("deviceSelectors").Index(1), "unknownSelector"),
			},
		},
		{
			name: "invalid-unused-device-selectors",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name:      "selector1",
							Selectors: []v1alpha1.Selector{{CEL: &v1alpha1.CELDeviceSelector{Expression: expression}}},
						},
						{
							Name:      "selector2",
							Selectors: []v1alpha1.Selector{{CEL: &v1alpha1.CELDeviceSelector{Expression: expression}}},
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Forbidden(field.NewPath("spec").Child("deviceSelectors").Index(1), "device selector must be referenced by at least one device configuration")},
		},
		{
			name: "valid-single-selector-multiple-device-configuration",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
						{
							Name:            "config2",
							DeviceSelectors: []string{"selector1"},
						},
					},
				},
			},
			want: field.ErrorList{},
		},
		{
			name: "valid-multiple-selector-single-device-configuration",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
						{
							Name: "selector2",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1", "selector2"},
						},
					},
				},
			},
			want: field.ErrorList{},
		},
		{
			name: "invalid-too-long-cel-selector",
			deviceNetwork: func() *v1alpha1.DeviceNetwork {
				expression := `device.pciRoot == ""`
				return &v1alpha1.DeviceNetwork{
					Spec: v1alpha1.DeviceNetworkSpec{
						DeviceSelectors: []v1alpha1.DeviceSelector{
							{
								Name:      "selector1",
								Selectors: []v1alpha1.Selector{v1alpha1.Selector{CEL: &v1alpha1.CELDeviceSelector{Expression: strings.ReplaceAll(expression, `""`, `"`+strings.Repeat("x", v1alpha1.CELSelectorExpressionMaxLength-len(expression)+1)+`"`)}}},
							},
						},
						DeviceConfigurations: []v1alpha1.DeviceConfiguration{
							{
								Name:            "config1",
								DeviceSelectors: []string{"selector1"},
							},
						},
					},
				}
			}(),
			want: field.ErrorList{
				field.TooLong(field.NewPath("spec").Child("deviceSelectors").Index(0).Child("selectors").Index(0).Child("cel").Child("expression"), "", v1alpha1.CELSelectorExpressionMaxLength),
			},
		},
		{
			name: "invalid-required-cel",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name:      "selector1",
							Selectors: []v1alpha1.Selector{{CEL: nil}},
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Required(field.NewPath("spec").Child("deviceSelectors").Index(0).Child("selectors").Index(0).Child("cel"), ""),
			},
		},
		{
			name: "invalid-device-configuration-not-referencing-device-selector",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Forbidden(field.NewPath("spec").Child("deviceSelectors").Index(0), "device selector must be referenced by at least one device configuration"),
				field.Required(field.NewPath("spec").Child("deviceConfigurations").Index(0).Child("deviceSelectors"), "must reference at least one device selector"),
			},
		},
		{
			name: "invalid-no-device-selector",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Forbidden(field.NewPath("spec").Child("deviceSelectors"), "must have at least one device selector"),
				field.Required(field.NewPath("spec").Child("deviceConfigurations").Index(0).Child("deviceSelectors"), "must reference at least one device selector"),
			},
		},
		{
			name: "invalid-no-device-configuration",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
					},
				},
			},
			want: field.ErrorList{
				field.Forbidden(field.NewPath("spec").Child("deviceSelectors").Index(0), "device selector must be referenced by at least one device configuration"),
				field.Forbidden(field.NewPath("spec").Child("deviceConfigurations"), "must have at least one device configuration"),
			},
		},
		{
			name: "invalid-host-device",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
							DeviceType:      &deviceTypeHostDevice,
							Macvlan:         &v1alpha1.Macvlan{},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Forbidden(field.NewPath("spec").Child("deviceConfigurations").Index(0).Child("macvlan"), "macvlan configuration is not allowed when deviceType is HostDevice"),
			},
		},
		{
			name: "invalid-macvlan",
			deviceNetwork: &v1alpha1.DeviceNetwork{
				Spec: v1alpha1.DeviceNetworkSpec{
					DeviceSelectors: []v1alpha1.DeviceSelector{
						{
							Name: "selector1",
						},
					},
					DeviceConfigurations: []v1alpha1.DeviceConfiguration{
						{
							Name:            "config1",
							DeviceSelectors: []string{"selector1"},
							DeviceType:      &deviceTypeMacvlan,
							HostDevice:      &v1alpha1.HostDevice{},
						},
					},
				},
			},
			want: field.ErrorList{
				field.Forbidden(field.NewPath("spec").Child("deviceConfigurations").Index(0).Child("hostDevice"), "hostDevice configuration is not allowed when deviceType is Macvlan"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validation.ValidateDeviceNetwork(tt.deviceNetwork)
			assertFailures(t, tt.want, got)
		})
	}
}

// AssertFailures compares the expected against the actual errors.
//
// If they differ, it also logs what the formatted errors would look
// like to a user. This can be helpful to figure out whether an error
// is informative.
func assertFailures(tb testing.TB, want, got field.ErrorList) bool {
	tb.Helper()
	if diff := cmp.Diff(want, got, cmpopts.IgnoreFields(field.Error{}, "Origin"), cmp.AllowUnexported(api.UniqueString{})); diff != "" {
		tb.Errorf("unexpected field errors (-want, +got):\n%s", diff)
		return false
	}
	return true
}
