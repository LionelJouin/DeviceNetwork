# Demo

This walks through deploying DeviceNetwork on a kind cluster, exposing a set of
host interfaces as devices, and attaching pods to them as a Macvlan and as a
raw HostDevice.

## Prerequisites

- A 2 node kind cluster with Kubernetes 1.36.4+ and NRI enabled on containerd.

## 1. Build and push the image

```sh
make push-image VERSION=latest
```

## 2. Create host interfaces to expose

DeviceNetwork selects interfaces on the node, so create some dummy ones.
`dummy1` is enslaved to a bridge to show that already-enslaved interfaces are
selected but cannot be exposed as a `host-device` (see [Output C](#c---resourceslices-after-creating-the-devicenetwork)).

```sh
docker exec -it kind-worker ip link add dummy0 type dummy
docker exec -it kind-worker ip link set dummy0 up

docker exec -it kind-control-plane ip link add dummy0 type dummy
docker exec -it kind-control-plane ip link set dummy0 up

docker exec -it kind-worker ip link add br0 type bridge
docker exec -it kind-worker ip link set br0 up
docker exec -it kind-worker ip link add dummy1 type dummy
docker exec -it kind-worker ip link set dummy1 master br0
docker exec -it kind-worker ip link set dummy1 up
```

## 3. Deploy DeviceNetwork

Installs the CRDs, the `DeviceClass`, RBAC and the `devicenetwork` DaemonSet
(DRA Driver + NRI plugin) on every node.

```sh
kubectl apply -f ./deployment
```

See [Output A](#a---devicenetwork-daemonset-pods): the `devicenetwork` pods are running on every node.

## 4. Create a DeviceNetwork

[`examples/example.yaml`](../examples/example.yaml) selects every `dummy*`
interface on non-control-plane nodes and exposes two configurations on top of
them: a `macvlan` device and a passthrough `host-device`. Random IPs from
`192.168.1.0/24` are assigned to whichever configuration a pod ends up using.

```sh
kubectl apply -f examples/example.yaml
```

See [Output B](#b---devicenetwork-resource): the `DeviceNetwork` resource is created.

Check that a device was published for each selected interface:

```sh
kubectl get resourceslices -o yaml
```

See [Output C](#c---resourceslices-after-creating-the-devicenetwork): `kind-control-plane` has no
devices published because it's excluded by `example.yaml`'s `nodeSelector`. On
`kind-worker`, only `dummy0` is exposed, as both a `macvlan` and a
`host-device` — `dummy1` is skipped for `host-device` because it's already
enslaved to `br0` and the kernel won't let it be moved into a pod's network
namespace. `dummy0`'s two configurations share a `mutual-exclusion` counter
(capacity `65536`): `macvlan` consumes `1` per allocation (allowing up to
`65535` concurrent Macvlans), while `host-device` consumes the whole `65536`,
guaranteeing it can only be claimed when no other configuration is using the
interface.

## 5. Attach workloads

[`examples/demo-macvlan.yaml`](../examples/demo-macvlan.yaml) deploys a pod
that claims a device through the `macvlan` configuration:

```sh
kubectl apply -f examples/demo-macvlan.yaml
```

[`examples/demo-host-device.yaml`](../examples/demo-host-device.yaml) deploys
a second pod that claims a device through the `host-device` configuration,
moving the host interface into the pod's network namespace:

```sh
kubectl apply -f examples/demo-host-device.yaml
```

See [Output D](#d---resourceclaims-after-attaching-both-workloads): `demo-macvlan` is
`Running`, having consumed `1` of `dummy0`'s `65536`-unit `mutual-exclusion`
counter. `demo-host-device` stays `Pending` because it needs the full `65536`
units to guarantee exclusive access to `dummy0`, and only `65535` remain while
`demo-macvlan`'s claim holds its share. It will get scheduled once the
`macvlan` claim is released.

## Example output

### A - DeviceNetwork DaemonSet pods

```sh
$ kubectl get pods -o wide
NAME                  READY   STATUS    RESTARTS   AGE   IP           NODE                 NOMINATED NODE   READINESS GATES
devicenetwork-9t9nl   1/1     Running   0          19s   172.18.0.2   kind-worker          <none>           <none>
devicenetwork-lcrnw   1/1     Running   0          19s   172.18.0.3   kind-control-plane   <none>           <none>
```

### B - DeviceNetwork resource

```sh
$ kubectl get devicenetwork
NAME         AGE
my-network   8s
```

### C - ResourceSlices after creating the DeviceNetwork

```sh
$ kubectl get resourceslice
NAME                                              NODE                 DRIVER             POOL                 AGE
00000-devicenetwork.io-kind-control-plane-fchfp   kind-control-plane   devicenetwork.io   kind-control-plane   40s
00000-devicenetwork.io-kind-worker-wnjft          kind-worker          devicenetwork.io   kind-worker          39s
00001-devicenetwork.io-kind-control-plane-4shpr   kind-control-plane   devicenetwork.io   kind-control-plane   40s
00001-devicenetwork.io-kind-worker-s9sgz          kind-worker          devicenetwork.io   kind-worker          39s
```

```yaml
$ kubectl get resourceslice -o yaml 00000-devicenetwork.io-kind-control-plane-fchfp
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  creationTimestamp: "2026-08-26T13:11:52Z"
  generateName: 00000-devicenetwork.io-kind-control-plane-
  generation: 1
  name: 00000-devicenetwork.io-kind-control-plane-fchfp
  ownerReferences:
  - apiVersion: v1
    controller: true
    kind: Node
    name: kind-control-plane
    uid: a831eca6-cdea-4b41-9874-c9fc2858d10c
  resourceVersion: "1744"
  uid: 8640404a-9662-453d-b336-19a58dd7deca
spec:
  driver: devicenetwork.io
  nodeName: kind-control-plane
  pool:
    generation: 1
    name: kind-control-plane
    resourceSliceCount: 2
```

```yaml
$ kubectl get resourceslice -o yaml 00001-devicenetwork.io-kind-control-plane-4shpr
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  creationTimestamp: "2026-08-26T13:11:52Z"
  generateName: 00001-devicenetwork.io-kind-control-plane-
  generation: 1
  name: 00001-devicenetwork.io-kind-control-plane-4shpr
  ownerReferences:
  - apiVersion: v1
    controller: true
    kind: Node
    name: kind-control-plane
    uid: a831eca6-cdea-4b41-9874-c9fc2858d10c
  resourceVersion: "1745"
  uid: 70136c6a-b8dd-439e-82cc-b150fd0d71f5
spec:
  driver: devicenetwork.io
  nodeName: kind-control-plane
  pool:
    generation: 1
    name: kind-control-plane
    resourceSliceCount: 2
```

```yaml
$ kubectl get resourceslice -o yaml 00000-devicenetwork.io-kind-worker-wnjft
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  creationTimestamp: "2026-08-26T13:11:53Z"
  generateName: 00000-devicenetwork.io-kind-worker-
  generation: 2
  name: 00000-devicenetwork.io-kind-worker-wnjft
  ownerReferences:
  - apiVersion: v1
    controller: true
    kind: Node
    name: kind-worker
    uid: 5139ec54-2e96-499f-8021-25c11d927ca7
  resourceVersion: "2152"
  uid: 7903c80c-7b00-4bed-8819-2a1ee27a9a62
spec:
  devices:
  - allowMultipleAllocations: true
    attributes:
      devicenetwork.io/deviceConfiguration:
        string: macvlan
      devicenetwork.io/deviceType:
        string: Macvlan
      devicenetwork.io/hostDeviceName:
        string: dummy0
      multinetwork.networking.k8s.io/networkKind:
        string: devicenetwork-io-devicenetwork
      multinetwork.networking.k8s.io/podNetwork:
        string: my-network
    capacity:
      devicenetwork.io/maxVirtualInterfaces:
        requestPolicy:
          default: "1"
          validValues:
          - "1"
        value: "65535"
    consumesCounters:
    - counterSet: dummy0
      counters:
        mutual-exclusion:
          value: "1"
    name: my-network-macvlan-dummy0
  - allowMultipleAllocations: false
    attributes:
      devicenetwork.io/deviceConfiguration:
        string: host-device
      devicenetwork.io/deviceType:
        string: HostDevice
      devicenetwork.io/hostDeviceName:
        string: dummy0
      multinetwork.networking.k8s.io/networkKind:
        string: devicenetwork-io-devicenetwork
      multinetwork.networking.k8s.io/podNetwork:
        string: my-network
    consumesCounters:
    - counterSet: dummy0
      counters:
        mutual-exclusion:
          value: "65536"
    name: my-network-host-device-dummy0
  driver: devicenetwork.io
  nodeName: kind-worker
  pool:
    generation: 2
    name: kind-worker
    resourceSliceCount: 2
```

```yaml
$ kubectl get resourceslice -o yaml 00001-devicenetwork.io-kind-worker-s9sgz
apiVersion: resource.k8s.io/v1
kind: ResourceSlice
metadata:
  creationTimestamp: "2026-08-26T13:11:53Z"
  generateName: 00001-devicenetwork.io-kind-worker-
  generation: 2
  name: 00001-devicenetwork.io-kind-worker-s9sgz
  ownerReferences:
  - apiVersion: v1
    controller: true
    kind: Node
    name: kind-worker
    uid: 5139ec54-2e96-499f-8021-25c11d927ca7
  resourceVersion: "2151"
  uid: c35c7002-5bc1-4963-a949-0dd38cc1127d
spec:
  driver: devicenetwork.io
  nodeName: kind-worker
  pool:
    generation: 2
    name: kind-worker
    resourceSliceCount: 2
  sharedCounters:
  - counters:
      mutual-exclusion:
        value: "65536"
    name: dummy0
```

### D - ResourceClaims after attaching both workloads

```sh
$ kubectl get pods -o wide
NAME                                READY   STATUS    RESTARTS   AGE     IP           NODE                 NOMINATED NODE   READINESS GATES
demo-host-device-5c7964ffdd-57rlr   0/1     Pending   0          75s     <none>       <none>               <none>           <none>
demo-macvlan-58b7bc847c-26gkz       1/1     Running   0          2m59s   10.244.1.2   kind-worker          <none>           <none>
```

```yaml
$ kubectl get resourceclaim -o yaml demo-macvlan-58b7bc847c-26gkz-my-network-macvlan-l7s94
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata:
  annotations:
    resource.kubernetes.io/pod-claim-name: my-network-macvlan
  creationTimestamp: "2026-08-26T13:18:23Z"
  finalizers:
  - resource.kubernetes.io/delete-protection
  generateName: demo-macvlan-58b7bc847c-26gkz-my-network-macvlan-
  name: demo-macvlan-58b7bc847c-26gkz-my-network-macvlan-l7s94
  namespace: default
  ownerReferences:
  - apiVersion: v1
    controller: true
    kind: Pod
    name: demo-macvlan-58b7bc847c-26gkz
    uid: 6d043df3-c1d6-44d6-9204-33b003883d5c
  resourceVersion: "2375"
  uid: 93f71307-b064-44bd-8cc3-340dc400afdd
spec:
  devices:
    requests:
    - exactly:
        allocationMode: ExactCount
        count: 1
        deviceClassName: devicenetwork
        selectors:
        - cel:
            expression: device.attributes["multinetwork.networking.k8s.io"].podNetwork
              == "my-network" && device.attributes["devicenetwork.io"].deviceConfiguration
              == "macvlan"
      name: my-network-macvlan
status:
  allocation:
    allocationTimestamp: "2026-08-26T13:18:23Z"
    devices:
      results:
      - consumedCapacity:
          devicenetwork.io/maxVirtualInterfaces: "1"
        device: my-network-macvlan-dummy0
        driver: devicenetwork.io
        pool: kind-worker
        request: my-network-macvlan
        shareID: ca26c4ae-4841-44c8-997e-d7fb961e1bea
    nodeSelector:
      nodeSelectorTerms:
      - matchFields:
        - key: metadata.name
          operator: In
          values:
          - kind-worker
  devices:
  - conditions: null
    data:
      device:
        apiVersion: devicenetwork.io/v1alpha1
        kind: Device
        metadata:
          name: dummy0
        spec:
          interfaceIndex: 3
          interfaceName: dummy0
      deviceConfiguration:
        deviceSelectors:
        - dummy-interfaces
        deviceType: Macvlan
        name: macvlan
    device: my-network-macvlan-dummy0
    driver: devicenetwork.io
    networkData:
      interfaceName: a0778231
      ips:
      - 192.168.1.191/24
    pool: kind-worker
    shareID: ca26c4ae-4841-44c8-997e-d7fb961e1bea
  reservedFor:
  - name: demo-macvlan-58b7bc847c-26gkz
    resource: pods
    uid: 6d043df3-c1d6-44d6-9204-33b003883d5c
```

```sh
$ kubectl get resourceclaim
NAME                                                             STATE                AGE
demo-host-device-5c7964ffdd-57rlr-my-network-host-device-4v9gh   pending              9s
demo-macvlan-58b7bc847c-26gkz-my-network-macvlan-l7s94           allocated,reserved   113s
```
