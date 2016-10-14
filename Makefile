GO_VERSION=1.7
GO=CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go
BIN=kube-scheduler
IMAGE=quay.io/molecule/$(BIN)
DOCKER_TAG=latest
IMAGE_TAG=$(IMAGE):$(DOCKER_TAG)

run:
	SCHEDULE_NAME=schedule.yml.sample go run kube-scheduler.go -logtostderr=true -v=2

build:
	$(GO) build -a -installsuffix cgo -o $(BIN) .

setup:
	glide install -v -s

docker: pre-docker-go-build build
	docker build -t $(IMAGE_TAG) .
	docker push $(IMAGE_TAG)

pre-docker-go-build:
	@if ! go version | grep $(GO_VERSION); then \
			echo "Must use go version $(GO_VERSION)!"; \
			exit 1; \
	fi 

.PHONY: clean

clean:
	rm $(BIN)
