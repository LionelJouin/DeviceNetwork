# DeviceNetwork


go test -v -cover -coverprofile cover.out ./...
go tool cover -html cover.out -o cover.html

make push-image VERSION=latest

docker exec -it kind-worker ip link add dummy0 type dummy
docker exec -it kind-worker ip link set dummy0 up




kubectl apply -f ./deployment

kubectl apply -f examples/example.yaml
kubectl apply -f examples/demo.yaml

kubectl delete -f ./deployment
kubectl delete -f examples/demo.yaml
