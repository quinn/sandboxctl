.PHONY: phase1-overlay-state-disk phase1-overlay-state-reset phase1-overlay-proof

phase1-overlay-state-disk:
	./tests/phase1/create-overlay-state-disk.sh

phase1-overlay-state-reset:
	./tests/phase1/reset-overlay-state-disk.sh

phase1-overlay-proof: download-builder-base-assets guest-initramfs phase1-overlay-state-disk
	@test -r /dev/kvm -a -w /dev/kvm || { echo 'Firecracker needs read and write access to /dev/kvm.' >&2; exit 1; }
	@test -r tmp/config/images/default/vmlinux || { echo 'Missing tmp/config/images/default/vmlinux.' >&2; exit 1; }
	@test -r tmp/config/images/default/ubuntu.squashfs || { echo 'Missing tmp/config/images/default/ubuntu.squashfs.' >&2; exit 1; }
	go run ./tests/phase1/overlay-proof --kernel tmp/config/images/default/vmlinux --initramfs tmp/config/initramfs/overlay-initramfs.cpio.gz --squashfs tmp/config/images/default/ubuntu.squashfs --state-disk tmp/config/state/overlay-proof.ext4
