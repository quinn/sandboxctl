.PHONY: e2e stage-bootstrap-test-image manual-bootstrap-test manual-builder-test manual-image-build manual-dockerfile-build manual-dockerfile-rejections manual-delete-test manual-reset-test

e2e:
	./e2e/run.sh

stage-bootstrap-test-image: download-builder-base-assets guest-initramfs
	./e2e/steps/stage-bootstrap-test-image.sh

manual-bootstrap-test:
	./e2e/steps/manual-bootstrap-test.sh

manual-builder-test:
	./e2e/steps/manual-builder-test.sh

manual-image-build:
	./e2e/steps/manual-image-build.sh

manual-dockerfile-build:
	./e2e/steps/manual-dockerfile-build.sh

manual-dockerfile-rejections:
	./e2e/steps/manual-dockerfile-rejections.sh

manual-delete-test:
	./e2e/steps/manual-delete-test.sh

manual-reset-test:
	RESET_TEST_IMAGE="$(if $(IMAGE),$(IMAGE),default)" ./e2e/steps/manual-reset-test.sh
