.PHONY: build test check download-test-assets download-overlay-assets manual-test overlay-initramfs overlay-state-disk overlay-state-reset overlay-proof clean

build:
	go build -o bin/sandboxctl ./cmd/sandboxctl

test:
	go test ./...

check:
	gofmt -w $$(find . -name '*.go' -type f)
	go vet ./...
	go test ./...

download-test-assets:
	REFRESH="$(REFRESH)" FIRECRACKER_CI_PREFIX="$(FIRECRACKER_CI_PREFIX)" ./scripts/download-test-assets.sh

download-overlay-assets:
	./scripts/download-overlay-assets.sh

manual-test: build
	./scripts/manual-vm-test.sh "$(if $(NAME),$(NAME),manualtest)"

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

clean:
	rm -rf bin dist
