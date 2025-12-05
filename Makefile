.PHONY: build docker push deploy clean test lint

VERSION ?= latest
DOCKER_REGISTRY ?= docker.io
DOCKER_IMAGE ?= $(DOCKER_REGISTRY)/redis-orchestrator
NAMESPACE ?= default

build:
	go build -o redis-orchestrator .

docker:
	docker build -t $(DOCKER_IMAGE):$(VERSION) .

push: docker
	docker push $(DOCKER_IMAGE):$(VERSION)

deploy:
	kubectl apply -f deploy/statefulset.yaml -n $(NAMESPACE)

clean:
	rm -f redis-orchestrator
	kubectl delete -f deploy/statefulset.yaml -n $(NAMESPACE) --ignore-not-found

test:
	go test -v ./...

lint:
	golangci-lint run

run-local:
	go run . \
		--redis-host=localhost \
		--redis-port=6379 \
		--redis-service=redis \
		--pod-name=test-pod-0 \
		--namespace=default \
		--label-selector=app=redis

