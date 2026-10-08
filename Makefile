.PHONY: build sandboxctl test check generate proto-lint download-test-assets download-overlay-assets manual-test manual-test-prepare overlay-initramfs overlay-state-disk overlay-state-reset overlay-proof stage-overlay-image clean

SANDBOXCTL_COMMANDS = create doctor help host host/install image image/install init list paths ssh ssh-config start stop version vmrun

build:
	mkdir -p bin
	go build -o bin/sandboxctl ./cmd/sandboxctl
	go build -o bin/sandboxd ./cmd/sandboxd
	for command in $(SANDBOXCTL_COMMANDS); do \
		binary_name=$$(printf '%s' "$$command" | tr '/' '-'); \
		go build -o "bin/sandboxctl-$$binary_name" "./cmd/sandboxctl/$$command" || exit $$?; \
	done

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

manual-test: build
	./scripts/manual-vm-test.sh "$(if $(NAME),$(NAME),manualtest)"

manual-test-prepare: build
	./scripts/prepare-manual-test-guest.sh "$(if $(NAME),$(NAME),manualtest)"

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

clean:
	rm -rf bin dist
