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

package device

import (
	"context"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
)

func EvalDevices(ctx context.Context, expression string, devices []*Device) ([]*Device, error) {
	program, err := celProgram(expression)
	if err != nil {
		return nil, fmt.Errorf("error creating CEL program: %w", err)
	}

	var filteredDevices []*Device
	for _, device := range devices {
		match, err := eval(ctx, program, device)
		if err != nil {
			return nil, fmt.Errorf("error evaluating CEL program: %w", err)
		}
		if match {
			filteredDevices = append(filteredDevices, device)
		}
	}

	return filteredDevices, nil
}

func EvalDevice(ctx context.Context, expression string, device *Device) (bool, error) {
	program, err := celProgram(expression)
	if err != nil {
		return false, fmt.Errorf("error creating CEL program: %w", err)
	}

	match, err := eval(ctx, program, device)
	if err != nil {
		return false, fmt.Errorf("error evaluating CEL program: %w", err)
	}

	return match, nil
}

func celProgram(expression string) (cel.Program, error) {
	env, err := cel.NewEnv(
		cel.Variable(string(v1alpha1.InterfaceNameDeviceSelectorAttribute), cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating CEL environment: %w", err)
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("type-check error: %w", issues.Err())
	}

	program, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("error creating CEL program: %w", err)
	}

	return program, nil
}

func eval(ctx context.Context, program cel.Program, device *Device) (bool, error) {
	out, _, err := program.ContextEval(ctx,
		map[string]any{
			string(v1alpha1.InterfaceNameDeviceSelectorAttribute): device.Spec.InterfaceName,
		},
	)
	if err != nil {
		return false, fmt.Errorf("error evaluating CEL program: %w", err)
	}

	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("unexpected result type: %T", out.Value())
	}

	return result, nil
}
