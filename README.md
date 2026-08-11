# DeviceNetwork

DeviceNetwork is a Kubernetes Network Driver (KND) that provides a declarative API to define what device-backed networks are available on a cluster and how to attach pods to them.

Beyond exposing bare devices, DeviceNetwork orchestrates how host interfaces are shared and subdivided, creating virtual devices (Macvlan, IPvlan...), tracking capacity and enforcing constraints across allocations.

This is a reference implementation for the [NetworkKind Multi-Network API](https://github.com/kubernetes-sigs/multi-network-api).

## Use Cases

- **Telco / 5G / 6G** -- CNFs typically require multiple secondary interfaces (Macvlan, IPvlan, Bond...) and accelerated data paths (PF/VF/SF, DPDK), all topology-aligned with other resources (CPU, Memory, Hugepages, accelerators...).
- **AI / ML / HPC** -- Training, inference and compute-intensive jobs need RDMA and SR-IOV interfaces co-located on the same PCI bus as GPUs to enable GPUDirect and low-latency inter-node communication.
- **Virtualization** -- VM workloads need PCI passthrough or SR-IOV VF assignment to expose physical network devices directly to guest operating systems.
- **Edge / MEC** -- Sites vary in physical wiring and available hardware. DeviceNetwork models per-node connectivity differences through device selectors, so workloads land where the right network exists without per-site configuration.

## How It Works

DeviceNetwork combines [DRA](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/) and [NRI](https://github.com/containerd/nri) with DRA handling scheduling and resource allocation and NRI hooking into container creation to configure the network interface in the pod.

A `DeviceNetwork` resource selects host interfaces and exposes them as DRA devices:

```yaml
apiVersion: devicenetwork.io/v1alpha1
kind: DeviceNetwork
metadata:
  name: my-network
spec:
  deviceSelectors:
  - name: "data-plane" # Select host interfaces whose name starts with "eno".
    selectors:
    - cel:
        expression: 'interfaceName.startsWith("eno")'
  deviceConfigurations:
  - name: "macvlan" # Create a Macvlan on top of a selected interface at pod startup.
    deviceType: Macvlan
    macvlan:
      mode: bridge
    deviceSelectors: # References the devices on which this configuration will apply. Here, the "data-plane" deviceSelector above.
    - "data-plane"
```

Pods claim a device through the standard Kubernetes `ResourceClaim` API. The scheduler places the pod on a node where a matching device is available, and NRI creates the interface in the pod's network namespace at startup according to the configuration defined in the `DeviceNetwork`:

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: my-network-attachment
spec:
  spec:
    devices:
      requests:
      - name: my-network # Request a device from the "my-network" DeviceNetwork.
        exactly:
          selectors:
          - cel: # podNetwork attribute is set by DeviceNetwork to the name of the DeviceNetwork resource.
              expression: device.attributes["multinetwork.networking.k8s.io"].podNetwork == "my-network"
---
apiVersion: v1
kind: Pod
metadata:
  name: my-pod
spec:
  resourceClaims:
  - name: my-network-attachment
    resourceClaimTemplateName: my-network-attachment
```

## Quick Start

Deploy DeviceNetwork and the CRD:

```bash
kubectl apply -f ./deployment
```

Create a `DeviceNetwork`:

```bash
kubectl apply -f examples/example.yaml
```

Deploy a workload that attaches to it:

```bash
kubectl apply -f examples/demo.yaml
```

## Community, discussion, contribution, and support

Kubernetes Multi-Network is sub-project within [SIG-Network](https://github.com/kubernetes/community/tree/master/sig-network).

Join the conversation:

- Agenda: https://docs.google.com/document/d/1pe_0aOsI35BEsQJ-FhFH9Z_pWQcU2uqwAnOx2NIx6OY/edit?tab=t.0
- Meeting: https://zoom.us/j/95680858961?pwd=M1c2TTdMZHpMUUtIYXRpbjRobkNJZz09
- Slack: [#sig-network-multi-network](https://kubernetes.slack.com/archives/C03UT5H9KDZ) on Kubernetes Slack

Learn how to engage with the Kubernetes community on the [community page](http://kubernetes.io/community/).

### Code of conduct

Participation in the Kubernetes community is governed by the [Kubernetes Code of Conduct](code-of-conduct.md).

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
