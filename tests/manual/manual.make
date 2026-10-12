.PHONY: stage-bootstrap-test-image

stage-bootstrap-test-image: download-builder-base-assets guest-initramfs
	./tests/manual/stage-bootstrap-test-image.sh
