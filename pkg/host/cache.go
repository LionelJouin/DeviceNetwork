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

package host

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jaypipes/ghw"
	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	"github.com/vishvananda/netlink"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	queueName = "device_cache"

	kind = "Device"
)

type DeviceCache struct {
	informer cache.SharedIndexInformer
	watcher  *watch.Broadcaster

	queue workqueue.TypedRateLimitingInterface[string]

	interval time.Duration

	// sysfsNetRoot is the sysfs path holding the network device class, used to
	// discover whether an interface is RDMA-capable. Defaults to
	// DefaultSysfsNetRoot; tests override it to point at a temporary directory.
	sysfsNetRoot string

	mu       sync.RWMutex
	snapshot map[string]*Device
}

// NewDeviceCache creates a DeviceCache. The source informers must not have
// been started yet (indexers must be added before start).
func NewDeviceCache(
	interval time.Duration,
) (*DeviceCache, error) {
	dc := &DeviceCache{
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[string](),
			workqueue.TypedRateLimitingQueueConfig[string]{Name: queueName},
		),
		interval:     interval,
		sysfsNetRoot: DefaultSysfsNetRoot,
		snapshot:     map[string]*Device{},
	}

	dc.watcher = watch.NewBroadcaster(1000, watch.DropIfChannelFull)

	dc.informer = cache.NewSharedIndexInformer(
		&listWatch{
			ListWatch: cache.ListWatch{
				ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
					devices := dc.List(ctx)
					deviceList := &DeviceList{
						TypeMeta: metav1.TypeMeta{
							Kind:       kind,
							APIVersion: v1alpha1.GroupVersion.String(),
						},
						ListMeta: metav1.ListMeta{},
						Items:    []Device{},
					}
					for _, d := range devices {
						if d == nil {
							continue
						}
						deviceList.Items = append(deviceList.Items, *d)
					}
					return deviceList, nil
				},
				WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
					return dc.watcher.Watch()
				},
			},
		},
		&Device{},
		0,
		cache.Indexers{},
	)

	return dc, nil
}

func (dc *DeviceCache) runWorker(ctx context.Context) {
	for dc.processNextWorkItem(ctx) {
	}
}

func (dc *DeviceCache) processNextWorkItem(ctx context.Context) bool {
	key, shutdown := dc.queue.Get()
	if shutdown {
		return false
	}
	defer dc.queue.Done(key)

	if err := dc.syncDevice(key); err != nil {
		klog.FromContext(ctx).Error(err, "Failed to sync device", "key", key)
		dc.queue.AddRateLimited(key)
		return true
	}

	dc.queue.Forget(key)
	return true
}

// Run starts the cache. It blocks until ctx is cancelled.
func (dc *DeviceCache) Run(ctx context.Context, workers int) error {
	if !cache.WaitForNamedCacheSyncWithContext(
		ctx,
	) {
		return fmt.Errorf("failed to wait for source informer caches to sync")
	}

	go dc.informer.RunWithContext(ctx)
	go dc.watchDevices(ctx)

	if !cache.WaitForCacheSync(ctx.Done(), dc.informer.HasSynced) {
		return fmt.Errorf("failed to wait for synthetic informer cache to sync")
	}

	var wg sync.WaitGroup
	defer func() {
		klog.FromContext(ctx).Info("Shutting down device cache")
		dc.queue.ShutDown()
		wg.Wait()
		dc.watcher.Shutdown()
	}()

	for i := 0; i < workers; i++ {
		wg.Go(func() {
			wait.UntilWithContext(ctx, dc.runWorker, time.Second)
		})
	}

	<-ctx.Done()
	return nil
}

// Informer returns the underlying SharedIndexInformer.
func (dc *DeviceCache) Informer() cache.SharedIndexInformer {
	return dc.informer
}

// Broadcaster returns the underlying Broadcaster.
func (dc *DeviceCache) Broadcaster() *watch.Broadcaster {
	return dc.watcher
}

// AddEventHandler registers an event handler with the synthetic informer.
func (dc *DeviceCache) AddEventHandler(handler cache.ResourceEventHandlerFuncs) (cache.ResourceEventHandlerRegistration, error) {
	return dc.informer.AddEventHandler(handler)
}

// HasSynced returns true once the cache has completed its initial sync.
func (dc *DeviceCache) HasSynced() bool {
	return dc.informer.HasSynced()
}

// Get returns the Device for a specific device key.
func (dc *DeviceCache) Get(name string) (*Device, error) {
	key := name

	obj, exists, err := dc.informer.GetStore().GetByKey(key)
	if err != nil {
		return nil, fmt.Errorf("error getting device for key %s: %w", key, err)
	}
	if !exists {
		return nil, fmt.Errorf("device for key %s not found", key)
	}

	device, ok := obj.(*Device)
	if !ok {
		return nil, fmt.Errorf("unexpected type for device for key %s", key)
	}

	return device, nil
}

// List returns all Devices matching the given options.
func (dc *DeviceCache) List(ctx context.Context, opts ...Option) []*Device {
	options := &listOption{}
	for _, opt := range opts {
		opt(options)
	}

	result := sets.Set[*Device]{}

	for _, obj := range dc.informer.GetStore().List() {
		device, ok := obj.(*Device)
		if !ok {
			continue
		}

		result.Insert(device)
	}

	for _, selector := range options.selectors {
		if selector.CEL == nil {
			continue
		}

		evalDevices, err := EvalDevices(ctx, selector.CEL.Expression, result.UnsortedList())
		if err != nil {
			klog.FromContext(ctx).Error(err, "Failed to evaluate device filter expression", "selector", selector.CEL.Expression)
			continue
		}

		result = sets.New(evalDevices...)
	}

	return result.UnsortedList()
}

func (dc *DeviceCache) syncDevice(deviceKey string) error {
	link, err := netlink.LinkByName(deviceKey)
	if err != nil {
		if !errors.As(err, &netlink.LinkNotFoundError{}) {
			return fmt.Errorf("failed to get device %s: %v", deviceKey, err)
		}

		dc.mu.Lock()
		existing, found := dc.snapshot[deviceKey]
		if found {
			delete(dc.snapshot, deviceKey)
		}
		dc.mu.Unlock()

		if found {
			if err := dc.informer.GetStore().Delete(existing); err != nil {
				return fmt.Errorf("failed to delete device %s from store: %v", deviceKey, err)
			}
			if err := dc.watcher.Action(watch.Deleted, existing); err != nil {
				return fmt.Errorf("failed to broadcast delete for device %s: %v", deviceKey, err)
			}
		}

		return nil
	}

	newEntry := dc.buildDevice(link)

	klog.FromContext(context.TODO()).Info("Syncing device", "key", deviceKey, "device", newEntry)

	dc.mu.Lock()
	existing, found := dc.snapshot[deviceKey]
	dc.snapshot[deviceKey] = newEntry
	dc.mu.Unlock()

	if found {
		if existing.Spec == newEntry.Spec {
			return nil
		}
		if err := dc.informer.GetStore().Update(newEntry); err != nil {
			return fmt.Errorf("failed to update device %s in store: %v", deviceKey, err)
		}
		if err := dc.watcher.Action(watch.Modified, newEntry); err != nil {
			return fmt.Errorf("failed to broadcast update for device %s: %v", deviceKey, err)
		}

		return nil
	}

	if err := dc.informer.GetStore().Add(newEntry); err != nil {
		return fmt.Errorf("failed to add device %s to store: %v", deviceKey, err)
	}
	if err := dc.watcher.Action(watch.Added, newEntry); err != nil {
		return fmt.Errorf("failed to broadcast add for device %s: %v", deviceKey, err)
	}

	return nil
}

func (dc *DeviceCache) buildDevice(link netlink.Link) *Device {
	result := &Device{
		TypeMeta: metav1.TypeMeta{
			Kind:       kind,
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: link.Attrs().Name,
		},
		Spec: DeviceSpec{
			InterfaceName:  link.Attrs().Name,
			InterfaceIndex: link.Attrs().Index,
			RDMACapable:    isRDMACapable(dc.sysfsNetRoot, link.Attrs().Name),
		},
	}

	return result
}

// listWatch wraps a ListerWatcher to opt out of the WatchList streaming
// protocol. Synthetic informers backed by a watch.Broadcaster do not support
// SendInitialEvents / bookmark signaling.
type listWatch struct {
	cache.ListWatch
}

func (listWatch) IsWatchListSemanticsUnSupported() bool { return true }

func (dc *DeviceCache) watchDevices(ctx context.Context) {
	ticker := time.NewTicker(dc.interval)
	defer ticker.Stop()

	dc.pollDevices()

	for {
		select {
		case <-ticker.C:
			dc.pollDevices()
		case <-ctx.Done():
			return
		}
	}
}

func (dc *DeviceCache) pollDevices() {
	netInfo, err := ghw.Network()
	if err != nil {
		return
	}

	for _, nic := range netInfo.NICs {
		dc.queue.Add(nic.Name)
	}
}
