.PHONY: build test check clean

build:
	go build -o bin/sandboxctl ./cmd/sandboxctl

test:
	go test ./...

check:
	gofmt -w $$(find . -name '*.go' -type f)
	go vet ./...
	go test ./...

clean:
	rm -rf bin dist

