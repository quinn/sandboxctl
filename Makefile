.PHONY: build install host-install sandboxctl test check generate proto-lint download-test-assets download-overlay-assets download-builder-tools manual-test manual-test-prepare manual-bootstrap-test manual-builder-test manual-image-build manual-delete-test overlay-initramfs overlay-state-disk overlay-state-reset overlay-proof stage-overlay-image stage-builder-image clean

SANDBOXCTL_COMMANDS = create delete doctor help host host/install image image/build image/install init list paths reset ssh ssh-config start stop version vmrun

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

download-test-assets:
	REFRESH="$(REFRESH)" FIRECRACKER_CI_PREFIX="$(FIRECRACKER_CI_PREFIX)" ./scripts/download-test-assets.sh

download-overlay-assets:
	./scripts/download-overlay-assets.sh

download-builder-tools:
	./scripts/download-builder-tools.sh

manual-test: build
	./scripts/manual-vm-test.sh "$(if $(NAME),$(NAME),manualtest)"

manual-test-prepare: build
	./scripts/prepare-manual-test-guest.sh "$(if $(NAME),$(NAME),manualtest)"

manual-bootstrap-test:
	./scripts/manual-bootstrap-test.sh

overlay-initramfs:
	./scripts/build-overlay-initramfs.sh

overlay-state-disk:
	./scripts/create-overlay-state-disk.sh

overlay-state-reset:
	./scripts/reset-overlay-state-disk.sh

overlay-proof: download-overlay-assets overlay-initramfs overlay-state-disk
	@test -r /dev/kvm -a -w /dev/kvm || { echo 'Firecracker needs read and write access to /dev/kvm.' >&2; exit 1; }
	@test -r tmp/config/images/default/vmlinux || { echo 'Missing tmp/config/images/default/vmlinux.' >&2; exit 1; }
	@test -r tmp/config/images/default/ubuntu.squashfs || { echo 'Missing tmp/config/images/default/ubuntu.squashfs.' >&2; exit 1; }
	go run ./cmd/overlay-proof --kernel tmp/config/images/default/vmlinux --initramfs tmp/config/initramfs/overlay-initramfs.cpio.gz --squashfs tmp/config/images/default/ubuntu.squashfs --state-disk tmp/config/state/overlay-proof.ext4

stage-overlay-image: download-overlay-assets overlay-initramfs
	./scripts/stage-overlay-image.sh

stage-builder-image: download-overlay-assets overlay-initramfs download-builder-tools
	./scripts/stage-builder-image.sh

manual-builder-test: build
	./scripts/manual-builder-test.sh

manual-image-build: build
	./scripts/manual-image-build.sh

manual-delete-test: build
	./scripts/manual-delete-test.sh

clean:
	rm -rf bin dist
