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
	"sync"
	"testing"
	"time"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	devicenetworkfake "github.com/lioneljouin/devicenetwork/pkg/client/clientset/versioned/fake"
	devicenetworkinformers "github.com/lioneljouin/devicenetwork/pkg/client/informers/externalversions"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeReconciler is a simple implementation of the reconciler interface for testing purposes.
// It records the number of times the Reconcile method is called.
type fakeReconciler struct {
	mu      sync.Mutex
	counter int
}

func (f *fakeReconciler) Reconcile(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counter++
	return nil
}

func (f *fakeReconciler) getReconcileCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counter
}

func newController(
	ctx context.Context,
	t *testing.T,
	initialDeviceNetworkObjects []runtime.Object,
	initialDeviceObjects []runtime.Object,
) (*devicenetworkfake.Clientset, *host.DeviceCache, *DeviceNetworkController, *fakeReconciler) {
	fakeKubeClient := fake.NewSimpleClientset()
	fakeDeviceNetworkClient := devicenetworkfake.NewSimpleClientset(initialDeviceNetworkObjects...)

	nodeInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(fakeKubeClient, 0)
	deviceNetworkInformers := devicenetworkinformers.NewSharedInformerFactory(fakeDeviceNetworkClient, 0)

	deviceCache, err := host.NewDeviceCache(time.Hour)
	if err != nil {
		t.Fatalf("failed to create device cache: %v", err)
	}
	for _, d := range initialDeviceObjects {
		if err := deviceCache.Informer().GetStore().Add(d); err != nil {
			t.Fatalf("failed to add device to cache: %v", err)
		}
	}

	controller, err := NewDeviceNetworkController(
		"node-a",
		"DeviceNetwork",
		deviceNetworkInformers.Devicenetwork().V1alpha1().DeviceNetworks(),
		nodeInformerFactory.Core().V1().Nodes(),
		deviceCache,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal("NewDeviceNetworkController failed:", err)
	}

	fr := &fakeReconciler{}
	controller.deviceNetworkReconciler = fr

	nodeInformerFactory.Start(ctx.Done())
	deviceNetworkInformers.Start(ctx.Done())
	go deviceCache.Informer().RunWithContext(ctx)

	return fakeDeviceNetworkClient, deviceCache, controller, fr
}

func TestEventHandlers(t *testing.T) {
	type object interface {
		runtime.Object
		metav1.Object
	}

	tests := []struct {
		name                        string
		initialDeviceNetworkObjects []runtime.Object
		initialDeviceObjects        []runtime.Object
		initialExpectedReconcile    int
		createObjects               []object
		updateObjects               []object
		deleteObjects               []object
		expectedReconcile           int
	}{
		{
			name:                     "create DeviceNetwork triggers reconcile",
			initialExpectedReconcile: 0,
			createObjects: []object{
				&v1alpha1.DeviceNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-dn",
					},
				},
			},
			expectedReconcile: 1,
		},
		{
			name: "update DeviceNetwork triggers reconcile",
			initialDeviceNetworkObjects: []runtime.Object{
				&v1alpha1.DeviceNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-dn",
					},
				},
			},
			initialExpectedReconcile: 1,
			updateObjects: []object{
				&v1alpha1.DeviceNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "test-dn",
						Labels: map[string]string{"updated": "true"},
					},
				},
			},
			expectedReconcile: 2,
		},
		{
			name: "delete DeviceNetwork triggers reconcile",
			initialDeviceNetworkObjects: []runtime.Object{
				&v1alpha1.DeviceNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-dn",
					},
				},
			},
			initialExpectedReconcile: 1,
			deleteObjects: []object{
				&v1alpha1.DeviceNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-dn",
					},
				},
			},
			expectedReconcile: 2,
		},
		{
			name:                     "create Device triggers reconcile",
			initialExpectedReconcile: 0,
			createObjects: []object{
				&host.Device{
					ObjectMeta: metav1.ObjectMeta{
						Name: "eth0",
					},
				},
			},
			expectedReconcile: 1,
		},
		{
			name: "update Device triggers reconcile",
			initialDeviceObjects: []runtime.Object{
				&host.Device{
					ObjectMeta: metav1.ObjectMeta{
						Name: "eth0",
					},
				},
			},
			initialExpectedReconcile: 1,
			updateObjects: []object{
				&host.Device{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "eth0",
						Labels: map[string]string{"updated": "true"},
					},
				},
			},
			expectedReconcile: 2,
		},
		{
			name: "delete Device triggers reconcile",
			initialDeviceObjects: []runtime.Object{
				&host.Device{
					ObjectMeta: metav1.ObjectMeta{
						Name: "eth0",
					},
				},
			},
			initialExpectedReconcile: 1,
			deleteObjects: []object{
				&host.Device{
					ObjectMeta: metav1.ObjectMeta{
						Name: "eth0",
					},
				},
			},
			expectedReconcile: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			fakeDeviceNetworkClient, deviceCache, deviceNetworkController, fr := newController(
				ctx,
				t,
				tt.initialDeviceNetworkObjects,
				tt.initialDeviceObjects,
			)

			go func() {
				_ = deviceNetworkController.Run(ctx, 1)
			}()

			err := wait.PollUntilContextTimeout(ctx, 1*time.Millisecond, 2*time.Second, true, func(context.Context) (bool, error) {
				return fr.getReconcileCount() == tt.initialExpectedReconcile, nil
			})
			if err != nil {
				t.Fatal("timed out waiting for initial reconcile:", err)
			}

			action := func(obj object, actionType string) {
				var gvr *schema.GroupVersionResource
				objectTracker := fakeDeviceNetworkClient.Tracker()

				switch obj.(type) {
				case *v1alpha1.DeviceNetwork:
					r := schema.GroupVersionResource{Group: v1alpha1.GroupVersion.Group, Version: v1alpha1.GroupVersion.Version, Resource: "devicenetworks"}
					gvr = &r
					objectTracker = fakeDeviceNetworkClient.Tracker()
				case *host.Device:
					store := deviceCache.Informer().GetStore()
					var storeErr error
					switch actionType {
					case "create":
						storeErr = store.Add(obj)
						deviceCache.Broadcaster().Action(watch.Added, obj)
					case "update":
						storeErr = store.Update(obj)
						deviceCache.Broadcaster().Action(watch.Modified, obj)
					case "delete":
						storeErr = store.Delete(obj)
						deviceCache.Broadcaster().Action(watch.Deleted, obj)
					}
					if storeErr != nil {
						t.Fatal("device action failed:", storeErr)
					}
					return
				}

				if gvr == nil {
					t.Fatal("gvr is nil")
				}

				var err error
				switch actionType {
				case "create":
					err = objectTracker.Create(*gvr, obj, obj.GetNamespace(), metav1.CreateOptions{})
				case "update":
					err = objectTracker.Update(*gvr, obj, obj.GetNamespace(), metav1.UpdateOptions{})
				case "delete":
					err = objectTracker.Delete(*gvr, obj.GetNamespace(), obj.GetName(), metav1.DeleteOptions{})
				}

				if err != nil {
					t.Fatal("action failed:", err)
				}
			}

			for _, object := range tt.createObjects {
				action(object, "create")
			}
			for _, object := range tt.updateObjects {
				action(object, "update")
			}
			for _, object := range tt.deleteObjects {
				action(object, "delete")
			}

			err = wait.PollUntilContextTimeout(ctx, 1*time.Millisecond, 2*time.Second, true, func(context.Context) (bool, error) {
				return fr.getReconcileCount() == tt.expectedReconcile, nil
			})
			if err != nil {
				t.Fatal("timed out waiting for reconcile after actions:", err)
			}

			actualReconcileCount := fr.getReconcileCount()
			if actualReconcileCount != tt.expectedReconcile {
				t.Errorf("processNextWorkItem() = %v, want %v", actualReconcileCount, tt.expectedReconcile)
			}
		})
	}
}
