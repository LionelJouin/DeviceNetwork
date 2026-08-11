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
package validation

import (
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/validation"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateDeviceNetwork validates a DeviceNetwork.
func ValidateDeviceNetwork(deviceNetwork *v1alpha1.DeviceNetwork) field.ErrorList {
	allErrs := field.ErrorList{}
	allErrs = append(allErrs, validateDeviceNetworkSpec(&deviceNetwork.Spec, field.NewPath("spec"))...)
	return allErrs
}

func validateDeviceNetworkSpec(deviceNetworkSpec *v1alpha1.DeviceNetworkSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList

	selectors := listSelectorNames(deviceNetworkSpec.DeviceSelectors)
	usedSelectors := listUsedSelectorNames(deviceNetworkSpec.DeviceConfigurations)

	if len(deviceNetworkSpec.DeviceSelectors) == 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("deviceSelectors"), "must have at least one device selector"))
	}

	allErrs = append(allErrs, validation.ValidateSet(deviceNetworkSpec.DeviceSelectors, v1alpha1.DeviceSelectorMaxSize,
		func(deviceSelector v1alpha1.DeviceSelector, fldPath *field.Path) field.ErrorList {
			return validateDeviceSelector(deviceSelector, fldPath, usedSelectors)
		},
		func(deviceSelector v1alpha1.DeviceSelector) string {
			return deviceSelector.Name
		},
		fldPath.Child("deviceSelectors"))...)

	if len(deviceNetworkSpec.DeviceConfigurations) == 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("deviceConfigurations"), "must have at least one device configuration"))
	}

	allErrs = append(allErrs, validation.ValidateSet(deviceNetworkSpec.DeviceConfigurations, v1alpha1.DeviceConfigurationMaxSize,
		func(deviceConfiguration v1alpha1.DeviceConfiguration, fldPath *field.Path) field.ErrorList {
			return validateDeviceConfiguration(deviceConfiguration, fldPath, selectors)
		},
		func(deviceConfiguration v1alpha1.DeviceConfiguration) string {
			return deviceConfiguration.Name
		},
		fldPath.Child("deviceConfigurations"))...)

	return allErrs
}

func validateDeviceSelector(deviceSelector v1alpha1.DeviceSelector, fldPath *field.Path, usedSelectors sets.Set[string]) field.ErrorList {
	var allErrs field.ErrorList

	if !usedSelectors.Has(deviceSelector.Name) {
		allErrs = append(allErrs, field.Forbidden(fldPath, "device selector must be referenced by at least one device configuration"))
	}

	allErrs = append(allErrs, validation.ValidateSlice(deviceSelector.Selectors, v1alpha1.SelectorPerDeviceSelectorMaxSize,
		func(selector v1alpha1.Selector, fldPath *field.Path) field.ErrorList {
			return validateSelector(selector, fldPath)
		},
		fldPath.Child("selectors"))...)

	if deviceSelector.NodeSelector != nil {
		allErrs = append(allErrs, validation.ValidateNodeSelector(deviceSelector.NodeSelector, false, fldPath.Child("nodeSelector"))...)
	}

	return allErrs
}

func validateSelector(selector v1alpha1.Selector, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if selector.CEL == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("cel"), ""))
	} else {
		allErrs = append(allErrs, validateCELSelector(*selector.CEL, fldPath.Child("cel"))...)
	}
	return allErrs
}

func validateCELSelector(celSelector v1alpha1.CELDeviceSelector, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	// envType := environment.NewExpressions

	if len(celSelector.Expression) > v1alpha1.CELSelectorExpressionMaxLength {
		allErrs = append(allErrs, field.TooLong(fldPath.Child("expression"), "" /*unused*/, v1alpha1.CELSelectorExpressionMaxLength))
		// Don't bother compiling too long expressions.
		return allErrs
	}

	// TODO
	// result := dracel.GetCompiler(dracel.Features{EnableConsumableCapacity: utilfeature.DefaultFeatureGate.Enabled(features.DRAConsumableCapacity)}).CompileCELExpression(celSelector.Expression, dracel.Options{EnvType: &envType})
	// if result.Error != nil {
	// 	allErrs = append(allErrs, convertCELErrorToValidationError(fldPath.Child("expression"), celSelector.Expression, result.Error))
	// } else if result.MaxCost > v1alpha1.CELSelectorExpressionMaxCost {
	// 	allErrs = append(allErrs, field.Forbidden(fldPath.Child("expression"), "too complex, exceeds cost limit"))
	// }

	return allErrs
}

func validateDeviceConfiguration(deviceConfiguration v1alpha1.DeviceConfiguration, fldPath *field.Path, selectors sets.Set[string]) field.ErrorList {
	var allErrs field.ErrorList

	if len(deviceConfiguration.DeviceSelectors) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("deviceSelectors"), "must reference at least one device selector"))
	}

	allErrs = append(allErrs, validation.ValidateSet(deviceConfiguration.DeviceSelectors, v1alpha1.SelectorPerDeviceConfigurationMaxSize,
		func(name string, fldPath *field.Path) field.ErrorList {
			if !selectors.Has(name) {
				return field.ErrorList{field.NotFound(fldPath, name)}
			}
			return nil
		},
		func(name string) string {
			return name
		},
		fldPath.Child("deviceSelectors"))...)

	deviceType := v1alpha1.GetDeviceType(deviceConfiguration)
	switch deviceType {
	case v1alpha1.DeviceTypeHostDevice:
		if deviceConfiguration.Macvlan != nil {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("macvlan"), "macvlan configuration is not allowed when deviceType is HostDevice"))
		}
	case v1alpha1.DeviceTypeMacvlan:
		if deviceConfiguration.HostDevice != nil {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("hostDevice"), "hostDevice configuration is not allowed when deviceType is Macvlan"))
		}
	default:
		allErrs = append(allErrs, field.Invalid(fldPath.Child("deviceType"), deviceConfiguration.DeviceType, "unsupported device type"))
	}

	return allErrs
}

func listSelectorNames(deviceSelectors []v1alpha1.DeviceSelector) sets.Set[string] {
	selectorNames := sets.New[string]()
	for _, deviceSelector := range deviceSelectors {
		selectorNames.Insert(deviceSelector.Name)
	}
	return selectorNames
}

func listUsedSelectorNames(deviceConfigurations []v1alpha1.DeviceConfiguration) sets.Set[string] {
	usedSelectorNames := sets.New[string]()
	for _, deviceConfiguration := range deviceConfigurations {
		for _, selectorName := range deviceConfiguration.DeviceSelectors {
			usedSelectorNames.Insert(selectorName)
		}
	}
	return usedSelectorNames
}
