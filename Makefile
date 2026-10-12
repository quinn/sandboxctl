.PHONY: build install host-install sandboxctl test check generate proto-lint download-builder-tools download-builder-base-assets guest-initramfs stage-builder-image clean

include tests/phase1/phase1.make
include e2e/e2e.make

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
	@changes=$$(git status --porcelain --untracked-files=all) || exit $$?; \
	if [ -n "$$changes" ]; then \
		echo 'make install: the checkout has uncommitted changes.' >&2; \
		echo 'Commit or remove them before installing.' >&2; \
		printf '%s\n' "$$changes" >&2; \
		exit 1; \
	fi
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

download-builder-base-assets:
	./scripts/download-builder-base-assets.sh

guest-initramfs:
	./scripts/build-guest-initramfs.sh

stage-builder-image: download-builder-base-assets guest-initramfs download-builder-tools
	./scripts/stage-builder-image.sh

clean:
	rm -rf bin dist
