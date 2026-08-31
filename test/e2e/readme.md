# End-to-end tests

Ginkgo specs that run against a **real, running cluster** with DeviceNetwork
already deployed. 

## Requirements

- Kubernetes ≥ 1.34 with DRA (`resource.k8s.io/v1`) enabled.
- NRI enabled in the container runtime.
- A schedulable node with a **spare** interface per test:
    - **`DeviceNetwork`**: a spare interface, via `--e2e.macvlan-node-name` and
    `--e2e.macvlan-interface-name`.
    - **`Macvlan`**: a spare interface, via `--e2e.macvlan-node-name` and
    `--e2e.macvlan-interface-name`.
    - **`HostDevice`**: a spare, movable interface, via `--e2e.hostdevice-node-name`
    and `--e2e.hostdevice-interface-name`.
    - **`HostDevice RDMA`** — a **hardware RDMA NIC** via `--e2e.hostdevice-node-name` (the interface is
    auto-selected as the RDMA-capable one).

## Deploy DeviceNetwork

Build the image, make it reachable by the cluster nodes, and apply the manifests:

```bash
make push-image REGISTRY=<your-registry> VERSION=<your-version>  # build + tags + pushes to your registry

kubectl apply -f ./deployment
kubectl set image daemonset/devicenetwork devicenetwork=<your-registry>/devicenetwork:<your-version>
kubectl rollout status daemonset/devicenetwork --timeout=120s
```

## Run

```bash
KUBECONFIG=/path/to/kubeconfig \
go test ./test/e2e/... -v -count=1 -ginkgo.v \
    --e2e.macvlan-node-name=<node> --e2e.macvlan-interface-name=<iface> \
    --e2e.hostdevice-node-name=<node> --e2e.hostdevice-interface-name=<iface>
```

All four flags are required. Filter specs with `-ginkgo.focus=` / `-ginkgo.skip=`
(regex matched against the full spec text):

```bash
-ginkgo.focus="HostDevice RDMA"    # run only the HostDevice RDMA spec
-ginkgo.focus="HostDevice"          # run HostDevice and HostDevice RDMA
-ginkgo.skip="HostDevice RDMA"      # run everything except HostDevice RDMA (no RDMA hardware)
-ginkgo.skip="RDMA|Macvlan"         # skip HostDevice RDMA and Macvlan
```

## QEMU quick start

`make e2e` provisions Alpine + k3s in QEMU, deploys, runs the suite, and tears
down. See `hack/e2e/e2e-qemu-cluster.sh` for prerequisites and manual
`up`/`deploy`/`kubeconfig`/`down` subcommands.

Some specs are skipped in QEMU because it cannot emulate the required hardware:

- **`HostDevice RDMA`** — needs a hardware RDMA NIC where the Ethernet and RDMA
  functions share a PCI device. QEMU can no longer emulate an RDMA HCA (`pvrdma`
  was removed in QEMU 9.1), and software RDMA (`rxe`, `siw`) exposes virtual
  devices with no PCI parent, so the required sysfs topology never exists.
