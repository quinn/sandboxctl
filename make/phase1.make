.PHONY: download-overlay-assets overlay-initramfs overlay-state-disk overlay-state-reset overlay-proof stage-overlay-image

download-overlay-assets:
	./scripts/download-overlay-assets.sh

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
