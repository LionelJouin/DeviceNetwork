go test -v -cover -coverprofile cover.out ./...
go tool cover -html cover.out -o cover.html

sudo env "PATH=$PATH:/usr/local/go/bin" go test ./pkg/configurators/...
sudo env "PATH=$PATH:/usr/local/go/bin" go test ./pkg/host/...


make push-image VERSION=latest

docker exec -it kind-worker ip link add dummy0 type dummy
docker exec -it kind-worker ip link set dummy0 up




kubectl apply -f ./deployment

kubectl apply -f examples/example.yaml
kubectl apply -f examples/demo.yaml

kubectl delete -f ./deployment
kubectl delete -f examples/demo.yaml

TODO: netdevsim



Allocate:
- Find what to create
RunPodSandbox
- Done the stuff
StartContainer
- refuses until RunPodSanbox async is done
PostStartContainer
