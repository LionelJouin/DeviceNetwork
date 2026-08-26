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

package cmd

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/lioneljouin/devicenetwork/apis/v1alpha1"
	devicenetworkclientset "github.com/lioneljouin/devicenetwork/pkg/client/clientset/versioned"
	devicenetworkinformers "github.com/lioneljouin/devicenetwork/pkg/client/informers/externalversions"
	"github.com/lioneljouin/devicenetwork/pkg/configurators"
	"github.com/lioneljouin/devicenetwork/pkg/controllers/devicenetwork"
	"github.com/lioneljouin/devicenetwork/pkg/driver"
	"github.com/lioneljouin/devicenetwork/pkg/host"
	"github.com/lioneljouin/devicenetwork/pkg/nri"
	"github.com/lioneljouin/devicenetwork/pkg/resolver"
	"github.com/lioneljouin/devicenetwork/pkg/store"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	defaultInformerResyncPeriod = 30 * time.Second
)

type runOptions struct {
	pluginName    string
	pluginIndex   string
	networkKind   string
	DRADriverName string
	NodeName      string
	verbosity     int
}

func newCmdRun() *cobra.Command {
	runOpts := &runOptions{}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the devicenetwork controller",
		Long:  `Run the devicenetwork controller`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOpts.run(cmd.Context())
		},
	}

	cmd.Flags().StringVar(
		&runOpts.pluginName,
		"plugin-name",
		"devicenetwork",
		"Plugin name to register to NRI.",
	)

	cmd.Flags().StringVar(
		&runOpts.pluginIndex,
		"plugin-index",
		"",
		"plugin index to register to NRI.",
	)

	cmd.Flags().StringVar(
		&runOpts.networkKind,
		"network-kind",
		"devicenetwork-io-devicenetwork",
		"Network kind.",
	)

	cmd.Flags().StringVar(
		&runOpts.DRADriverName,
		"dra-driver-name",
		"devicenetwork.io",
		"DRA Driver Name.",
	)

	cmd.Flags().StringVar(
		&runOpts.NodeName,
		"node-name",
		"",
		"Node Name.",
	)

	cmd.Flags().IntVar(
		&runOpts.verbosity,
		"verbosity",
		0,
		"Log Level.",
	)

	return cmd
}

func (ro *runOptions) run(ctx context.Context) error {
	klog.InitFlags(nil)
	_ = flag.Set("v", fmt.Sprintf("%d", ro.verbosity))
	flag.Parse()

	clientCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to InClusterConfig: %v", err)
	}

	deviceNetworkClient, err := devicenetworkclientset.NewForConfig(clientCfg)
	if err != nil {
		return fmt.Errorf("failed to NewForConfig: %v", err)
	}

	kubeClient, err := kubernetes.NewForConfig(clientCfg)
	if err != nil {
		return fmt.Errorf("failed to NewForConfig: %v", err)
	}

	deviceNetworkInformerFactory := devicenetworkinformers.NewSharedInformerFactory(deviceNetworkClient, defaultInformerResyncPeriod)
	nodeInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClient, defaultInformerResyncPeriod, kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", ro.NodeName).String()
	}))
	resourceSliceInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClient, defaultInformerResyncPeriod, kubeinformers.WithTweakListOptions(func(options *metav1.ListOptions) {
		options.FieldSelector = fields.AndSelectors(
			fields.OneTermEqualSelector("spec.driver", ro.DRADriverName),
			fields.OneTermEqualSelector("spec.nodeName", ro.NodeName),
		).String()
	}))

	deviceCache, err := host.NewDeviceCache(30 * time.Second)
	if err != nil {
		return fmt.Errorf("failed to create device cache: %v", err)
	}

	resolver, err := resolver.NewResolver(
		ro.networkKind,
		resourceSliceInformerFactory.Resource().V1().ResourceSlices(),
		deviceNetworkInformerFactory.Devicenetwork().V1alpha1().DeviceNetworks(),
		deviceCache,
	)
	if err != nil {
		return fmt.Errorf("failed to create resolver: %v", err)
	}

	memoryStore := store.NewMemory()

	macvlanConfigurator := &configurators.Macvlan{
		CommonConfigurator: &configurators.CommonConfigurator{},
	}
	deviceConfigurators := map[v1alpha1.DeviceType]configurators.Configurator{
		v1alpha1.DeviceTypeMacvlan: macvlanConfigurator,
	}

	nriPlugin := nri.NewPlugin(
		ro.pluginName,
		ro.pluginIndex,
		ro.DRADriverName,
		memoryStore,
		deviceConfigurators,
	)

	draDriver, err := driver.Start(
		ctx,
		ro.DRADriverName,
		ro.NodeName,
		kubeClient,
		memoryStore,
		resolver,
		deviceConfigurators,
	)
	if err != nil {
		return fmt.Errorf("failed to dra.Start: %v", err)
	}
	defer draDriver.Stop()

	deviceNetworkController, err := devicenetwork.NewDeviceNetworkController(
		ro.NodeName,
		ro.networkKind,
		deviceNetworkInformerFactory.Devicenetwork().V1alpha1().DeviceNetworks(),
		nodeInformerFactory.Core().V1().Nodes(),
		deviceCache,
		draDriver.PublishResources,
		deviceConfigurators,
	)
	if err != nil {
		return fmt.Errorf("failed to create device network controller: %v", err)
	}

	deviceNetworkInformerFactory.Start(ctx.Done())
	nodeInformerFactory.Start(ctx.Done())
	resourceSliceInformerFactory.Start(ctx.Done())

	deviceNetworkInformerFactory.WaitForCacheSync(ctx.Done())
	nodeInformerFactory.WaitForCacheSync(ctx.Done())
	resourceSliceInformerFactory.WaitForCacheSync(ctx.Done())

	go func() {
		err = resolver.Run(ctx, 1)
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			klog.FromContext(ctx).Error(err, "failed to run resolver")
		}
	}()

	go func() {
		err = deviceCache.Run(ctx, 1)
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			klog.FromContext(ctx).Error(err, "failed to run device cache")
		}
	}()

	go func() {
		err = deviceNetworkController.Run(ctx, 1)
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			klog.FromContext(ctx).Error(err, "failed to run device network controller")
		}
	}()

	go func() {
		err = nriPlugin.Run(ctx)
		if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
			klog.FromContext(ctx).Error(err, "failed to run NRI plugin")
		}
	}()

	<-ctx.Done()

	return nil
}
