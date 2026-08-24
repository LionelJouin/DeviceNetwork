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

package nri

import (
	"context"
	"fmt"

	"github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// PodResourceStore is an interface to get and delete resource claims associated with a pod.
// It is used by the NRI plugin to manage resource claims for pods when the pod is created and deleted.
type PodResourceStore interface {
	Get(podUID types.UID) []*resourcev1.ResourceClaim
	Delete(podUID types.UID)
}

type Plugin struct {
	stub                   stub.Stub
	pluginName             string
	pluginIndex            string
	driverName             string
	podResourceStore       PodResourceStore
	deviceConfigurators    map[v1alpha1.DeviceType]configurators.Configurator
	configurationProcesses map[types.UID]*configurationProcess
	runContext             context.Context
}

func NewPlugin(
	pluginName string,
	pluginIndex string,
	driverName string,
	podResourceStore PodResourceStore,
	deviceConfigurators map[v1alpha1.DeviceType]configurators.Configurator,
) *Plugin {
	return &Plugin{
		pluginName:             pluginName,
		pluginIndex:            pluginIndex,
		driverName:             driverName,
		podResourceStore:       podResourceStore,
		deviceConfigurators:    deviceConfigurators,
		configurationProcesses: make(map[types.UID]*configurationProcess),
	}
}

func (p *Plugin) Run(ctx context.Context) error {
	opts := []stub.Option{
		stub.WithPluginName(p.pluginName),
		stub.WithPluginIdx(p.pluginIndex),
	}

	var err error
	p.stub, err = stub.New(p, opts...)
	if err != nil {
		return fmt.Errorf("failed to create plugin stub: %v", err)
	}

	p.runContext = ctx

	err = p.stub.Run(ctx)
	if err != nil {
		return fmt.Errorf("plugin exited with error: %v", err)
	}

	return nil
}

func (p *Plugin) RunPodSandbox(
	ctx context.Context,
	pod *api.PodSandbox,
) error {
	klog.FromContext(ctx).Info("RunPodSandbox", "pod.Name", pod.Name, "pod.Namespace", pod.Namespace)

	podNetworkNamespace := getNetworkNamespace(pod)
	if podNetworkNamespace == "" {
		return fmt.Errorf("error getting network namespace for pod '%s' in namespace '%s'", pod.Name, pod.Namespace)
	}

	_ = p.podResourceStore.Get(types.UID(pod.Uid))

	process := newConfigurationProcess(
		pod,
		podNetworkNamespace,
		p.podResourceStore.Get(types.UID(pod.Uid)),
		p.driverName,
		p.deviceConfigurators,
	)
	p.configurationProcesses[types.UID(pod.Uid)] = process

	go process.run(p.runContext)

	return nil
}

// func (p *Plugin) CreateContainer(
// 	ctx context.Context,
// 	pod *api.PodSandbox,
// 	ctr *api.Container,
// ) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
// 	klog.FromContext(ctx).Info("CreateContainer", "pod.Name", pod.Name, "pod.Namespace", pod.Namespace, "ctr.Name", ctr.Name, "ctr.Id", ctr.Id)

// 	return nil, nil, nil
// }

// StartContainer is called when a container is started.
// It checks if the device networks are all configured before allowing the container to start.
// If the device networks are not configured, it returns an error and the container will not be started.
func (p *Plugin) StartContainer(
	ctx context.Context,
	pod *api.PodSandbox,
	ctr *api.Container,
) error {
	klog.FromContext(ctx).Info("StartContainer", "pod.Name", pod.Name, "pod.Namespace", pod.Namespace, "ctr.Name", ctr.Name, "ctr.Id", ctr.Id)

	process, exists := p.configurationProcesses[types.UID(pod.Uid)]
	if !exists {
		return nil
	}

	if !process.isDone() {
		return fmt.Errorf("device networks are not configured for pod '%s' in namespace '%s'", pod.Name, pod.Namespace)
	}

	delete(p.configurationProcesses, types.UID(pod.Uid))

	return nil
}

func getNetworkNamespace(pod *api.PodSandbox) string {
	for _, namespace := range pod.Linux.GetNamespaces() {
		if namespace.Type == "network" {
			return namespace.Path
		}
	}

	return ""
}

func (p *Plugin) RemovePodSandbox(
	ctx context.Context,
	pod *api.PodSandbox,
) error {
	klog.FromContext(ctx).Info("RemovePodSandbox", "pod.Name", pod.Name, "pod.Namespace", pod.Namespace)

	process, exists := p.configurationProcesses[types.UID(pod.Uid)]
	if !exists {
		return nil
	}

	process.terminate()
	delete(p.configurationProcesses, types.UID(pod.Uid))

	return nil
}
