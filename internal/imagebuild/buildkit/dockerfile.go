// Package buildkit creates deterministic BuildKit build inputs.
package buildkit

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var pinnedImagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

const maxProvisioningScriptSize = 1 << 20

// Dockerfile returns a BuildKit Dockerfile that applies the provisioning
// script to a digest-pinned base image.
func Dockerfile(baseImage string) (string, error) {
	if !pinnedImagePattern.MatchString(baseImage) {
		return "", errors.New("base image must use a SHA-256 digest")
	}
	return fmt.Sprintf(`FROM %s
COPY provision.sh /tmp/sandboxctl-provision.sh
RUN ["/bin/sh", "-c", "/bin/sh /tmp/sandboxctl-provision.sh && rm -f /tmp/sandboxctl-provision.sh"]
`, baseImage), nil
}

// ContextFiles returns the fixed build context required by Dockerfile.
func ContextFiles(baseImage, provisioningScript string) (map[string][]byte, error) {
	if strings.TrimSpace(provisioningScript) == "" {
		return nil, errors.New("provisioning script is empty")
	}
	if len(provisioningScript) > maxProvisioningScriptSize {
		return nil, errors.New("provisioning script exceeds the size limit")
	}
	dockerfile, err := Dockerfile(baseImage)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"Dockerfile": []byte(dockerfile), "provision.sh": []byte(provisioningScript)}, nil
}
