.PHONY: build install release host-install sandboxctl test check generate proto-lint download-builder-tools download-builder-base-assets guest-initramfs stage-builder-image clean-builder clean

include tests/phase1/phase1.make
include e2e/e2e.make

SANDBOXCTL_COMMANDS = create delete doctor forward help host host/install image image/build image/delete image/install image/list init list paths pull reset ssh ssh-config start stop version vmrun
SANDBOXCTL_BUILD_VERSION ?= 0.1.0-dev
SANDBOXCTL_LDFLAGS = -X sandboxctl/internal/buildinfo.Version=$(SANDBOXCTL_BUILD_VERSION)

build:
	mkdir -p bin
	go build -ldflags "$(SANDBOXCTL_LDFLAGS)" -o bin/sandboxctl ./cmd/sandboxctl
	go build -ldflags "$(SANDBOXCTL_LDFLAGS)" -o bin/sandboxd ./cmd/sandboxd
	for command in $(SANDBOXCTL_COMMANDS); do \
		binary_name=$$(printf '%s' "$$command" | tr '/' '-'); \
		go build -ldflags "$(SANDBOXCTL_LDFLAGS)" -o "bin/sandboxctl-$$binary_name" "./cmd/sandboxctl/$$command" || exit $$?; \
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

release:
	./scripts/build-release.sh "$(TAG)"

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

clean-builder:
	./scripts/stage-builder-image.sh --clean

check-builder:
	./scripts/stage-builder-image.sh --check

stage-builder-image:
	./scripts/stage-builder-image.sh

clean:
	rm -rf bin dist
