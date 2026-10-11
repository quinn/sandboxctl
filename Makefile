.PHONY: build install host-install sandboxctl test check generate proto-lint download-builder-tools manual-bootstrap-test manual-builder-test manual-image-build manual-dockerfile-build manual-dockerfile-rejections manual-delete-test manual-reset-test stage-builder-image clean

include make/phase1.make

SANDBOXCTL_COMMANDS = create delete doctor help host host/install image image/build image/delete image/install image/list init list paths reset ssh ssh-config start stop version vmrun

build:
	mkdir -p bin
	go build -o bin/sandboxctl ./cmd/sandboxctl
	go build -o bin/sandboxd ./cmd/sandboxd
	for command in $(SANDBOXCTL_COMMANDS); do \
		binary_name=$$(printf '%s' "$$command" | tr '/' '-'); \
		go build -o "bin/sandboxctl-$$binary_name" "./cmd/sandboxctl/$$command" || exit $$?; \
	done

install:
	yay -Bi "$(CURDIR)/packaging/local"

host-install: build
	sudo env PATH="$(CURDIR)/bin:$$PATH" "$(CURDIR)/bin/sandboxctl" host install --operator "$$(id -un)"

sandboxctl: build
	PATH="$(CURDIR)/bin:$$PATH" "$(CURDIR)/bin/sandboxctl" $(ARGS)

test:
	go test ./...

check:
	gofmt -w $$(find . -name '*.go' -type f)
	go vet ./...
	go test ./...
	buf lint

generate:
	buf generate

proto-lint:
	buf lint

download-builder-tools:
	./scripts/download-builder-tools.sh

manual-bootstrap-test:
	./scripts/manual-bootstrap-test.sh


stage-builder-image: download-overlay-assets overlay-initramfs download-builder-tools
	./scripts/stage-builder-image.sh

manual-builder-test: build
	./scripts/manual-builder-test.sh

manual-image-build: build
	./scripts/manual-image-build.sh

manual-dockerfile-build: build
	./scripts/manual-dockerfile-build.sh builder-1791682822-1720510 sha256:040909f5da1acf7c6be8b9bbb490540126439e839c36d76aa7085e55197c2c7d

manual-dockerfile-rejections: build
	./scripts/manual-dockerfile-rejections.sh builder-1791682822-1720510 sha256:040909f5da1acf7c6be8b9bbb490540126439e839c36d76aa7085e55197c2c7d

manual-delete-test: build
	./scripts/manual-delete-test.sh

manual-reset-test: build
	RESET_TEST_IMAGE="$(if $(IMAGE),$(IMAGE),default)" ./scripts/manual-reset-test.sh

clean:
	rm -rf bin dist
