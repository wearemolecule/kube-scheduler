GO=CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go
BIN=kube-scheduler
LDFLAGS=-ldflags "-X main.buildstamp=`date -u '+%Y-%m-%d_%I:%M:%S%p'` -X main.githash=`git describe --tags --dirty`"

run:
	SCHEDULE_NAME=schedule.yml.sample go run kube-scheduler.go -logtostderr=true -v=2

build:
	$(GO) build -a -installsuffix cgo -o $(BIN) $(LDFLAGS) .

setup:
	dep ensure

.PHONY: clean

clean:
	rm $(BIN)
