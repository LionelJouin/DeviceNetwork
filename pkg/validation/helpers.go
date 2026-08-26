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
package validation

import (
	"slices"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// validationOption is an option for validation.
type validationOption int

const (
	// The validation of each item is covered by declarative validation.
	itemsCovered validationOption = iota
	// The list size check is covered by declarative validation.
	sizeCovered
	// The uniqueness check is covered by declarative validation.
	uniquenessCovered
)

// validateItems validates each item in a slice.
func ValidateItems[T any](slice []T, validateItem func(T, *field.Path) field.ErrorList, fldPath *field.Path, opts ...validationOption) field.ErrorList {
	var allErrs field.ErrorList
	for i, item := range slice {
		idxPath := fldPath.Index(i)
		errs := validateItem(item, idxPath)
		if slices.Contains(opts, itemsCovered) {
			errs = errs.MarkCoveredByDeclarative()
		}
		allErrs = append(allErrs, errs...)
	}
	return allErrs
}

// validateSet ensures that a slice contains no duplicates, does not
// exceed a certain maximum size and that all entries are valid.
func ValidateSet[T any, K comparable](slice []T, maxSize int, validateItem func(item T, fldPath *field.Path) field.ErrorList, itemKey func(T) K, fldPath *field.Path, opts ...validationOption) field.ErrorList {
	if maxSize >= 0 && len(slice) > maxSize {
		// Dumping the entire field into the error message is likely to be too long,
		// in particular when it is already beyond the maximum size. Instead this
		// just shows the number of entries.
		err := field.TooMany(fldPath, len(slice), maxSize).WithOrigin("maxItems")
		if slices.Contains(opts, sizeCovered) {
			err = err.MarkCoveredByDeclarative()
		}
		// maxSize check short-circuits for DOS protection
		return field.ErrorList{err}
	}

	allErrs := ValidateItems(slice, validateItem, fldPath, opts...)

	allItems := sets.New[K]()
	for i, item := range slice {
		idxPath := fldPath.Index(i)
		key := itemKey(item)
		childPath := idxPath
		if allItems.Has(key) {
			err := field.Duplicate(childPath, key)
			if slices.Contains(opts, uniquenessCovered) {
				err = err.MarkCoveredByDeclarative()
			}
			allErrs = append(allErrs, err)
		} else {
			allItems.Insert(key)
		}
	}
	return allErrs
}

// ValidateSlice ensures that a slice does not exceed a certain maximum size
// and that all entries are valid.
// A negative maxSize disables the length check.
func ValidateSlice[T any](slice []T, maxSize int, validateItem func(T, *field.Path) field.ErrorList, fldPath *field.Path, opts ...validationOption) field.ErrorList {
	if maxSize >= 0 && len(slice) > maxSize {
		// Dumping the entire field into the error message is likely to be too long,
		// in particular when it is already beyond the maximum size. Instead this
		// just shows the number of entries.
		err := field.TooMany(fldPath, len(slice), maxSize).WithOrigin("maxItems")
		if slices.Contains(opts, sizeCovered) {
			err = err.MarkCoveredByDeclarative()
		}
		// maxSize check short-circuits for DOS protection
		return field.ErrorList{err}
	}
	return ValidateItems(slice, validateItem, fldPath, opts...)
}
