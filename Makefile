.PHONY: build test lint tidy generate

build:
	go build ./...

test:
	go test ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

generate:
	go generate ./...
