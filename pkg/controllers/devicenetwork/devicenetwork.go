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

package devicenetwork

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	v1alpha1devicenetworkinformers "github.com/lioneljouin/devicenetwork/pkg/client/informers/externalversions/apis/v1alpha1"
	"github.com/lioneljouin/devicenetwork/pkg/device"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	queueName = "device_network"
)

type DeviceNetworkController struct {
	queue workqueue.TypedRateLimitingInterface[string]

	deviceNetworkReconciler *DeviceNetworkReconciler

	deviceNetworkSynced cache.InformerSynced
	deviceCacheSynced   cache.InformerSynced
	nodeSynced          cache.InformerSynced
}

func NewDeviceNetworkController(
	nodeName string,
	deviceNetworkInformer v1alpha1devicenetworkinformers.DeviceNetworkInformer,
	nodeInformer coreinformers.NodeInformer,
	deviceCache *device.DeviceCache,
	publishResourcesFunc PublishResources,
	deviceConfigurators map[v1alpha1.DeviceType]DeviceConfigurator,
) (*DeviceNetworkController, error) {
	dnc := &DeviceNetworkController{
		deviceNetworkSynced: deviceNetworkInformer.Informer().HasSynced,
		deviceCacheSynced:   deviceCache.Informer().HasSynced,
		nodeSynced:          nodeInformer.Informer().HasSynced,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: queueName},
		),
	}

	deviceNetworkReconciler, err := NewDeviceNetworkReconciler(
		nodeName,
		nodeInformer.Lister(),
		deviceNetworkInformer.Lister(),
		publishResourcesFunc,
		deviceCache,
		deviceConfigurators,
	)
	if err != nil {
		return nil, err
	}
	dnc.deviceNetworkReconciler = deviceNetworkReconciler

	if _, err := deviceNetworkInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { dnc.onEvent() },
		UpdateFunc: func(_, _ interface{}) { dnc.onEvent() },
		DeleteFunc: func(_ interface{}) { dnc.onEvent() },
	}); err != nil {
		return nil, err
	}

	if _, err := deviceCache.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(_ interface{}) { dnc.onEvent() },
		UpdateFunc: func(_, _ interface{}) { dnc.onEvent() },
		DeleteFunc: func(_ interface{}) { dnc.onEvent() },
	}); err != nil {
		return nil, err
	}

	return dnc, nil
}

func (dnc *DeviceNetworkController) Run(ctx context.Context, workers int) error {
	if !cache.WaitForNamedCacheSyncWithContext(
		ctx,
		dnc.deviceNetworkSynced,
		dnc.deviceCacheSynced,
		dnc.nodeSynced,
	) {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	var wg sync.WaitGroup
	defer func() {
		klog.FromContext(ctx).Info("Shutting down device network controller")
		dnc.queue.ShutDown()
		wg.Wait()
	}()

	for i := 0; i < workers; i++ {
		wg.Go(func() {
			wait.UntilWithContext(ctx, dnc.runWorker, time.Second)
		})
	}

	<-ctx.Done()

	return nil
}

func (dnc *DeviceNetworkController) runWorker(ctx context.Context) {
	for dnc.processNextWorkItem(ctx) {
	}
}

func (dnc *DeviceNetworkController) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := dnc.queue.Get()
	if shutdown {
		return false
	}
	defer dnc.queue.Done(key)

	err := dnc.deviceNetworkReconciler.Reconcile(ctx)
	if err == nil {
		dnc.queue.Forget(key)
		return true
	}

	runtime.HandleErrorWithContext(ctx, err, "Work item failed", "item", key)
	dnc.queue.AddRateLimited(key)

	return true
}

func (dnc *DeviceNetworkController) onEvent() {
	dnc.queue.Add("Event")
}
