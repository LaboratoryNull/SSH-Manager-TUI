.PHONY: build test fmt

build:
	go build -o ssh-manager .

test:
	go test ./...

fmt:
	gofmt -w .
