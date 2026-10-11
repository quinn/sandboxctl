// Package buildkit creates deterministic BuildKit build inputs.
package buildkit

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"sandboxctl/cmd/sandboxctl/image/build/distro"
	"sandboxctl/cmd/sandboxctl/image/build/provision"
)

var pinnedImagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[0-9a-f]{64}$`)

const maxProvisioningScriptSize = 1 << 20

// BuildKit applies the last matching ALLOW or DENY rule. Keep the digest-pinned
// image allowance after the broad image denial.
const sourcePolicy = `{"version":1,"rules":[{"action":"DENY","selector":{"identifier":"docker-image://*"}},{"action":"ALLOW","selector":{"identifier":"docker-image://*@sha256:*"}},{"action":"DENY","selector":{"identifier":"http://*"}},{"action":"DENY","selector":{"identifier":"https://*"}},{"action":"DENY","selector":{"identifier":"git://*"}}]}`

// Dockerfile returns a BuildKit Dockerfile that applies the provisioning
// script to a digest-pinned base image.
func Dockerfile(baseImage string) (string, error) {
	if !pinnedImagePattern.MatchString(baseImage) {
		return "", errors.New("base image must use a SHA-256 digest")
	}
	return fmt.Sprintf("FROM %s\n", baseImage), nil
}

// ProvisioningDockerfile returns a clean root stage that imports a user image
// and applies only sandboxctl's fixed guest provisioning script.
func ProvisioningDockerfile() string {
	return `FROM scratch
COPY --from=sandboxctl-user-image / /
USER 0:0
WORKDIR /
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
COPY provision.sh /tmp/sandboxctl-provision.sh
RUN ["/bin/sh", "-c", "/bin/sh /tmp/sandboxctl-provision.sh && rm -f /tmp/sandboxctl-provision.sh"]
`
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
	return map[string][]byte{
		"Dockerfile":           []byte(dockerfile),
		"provision.Dockerfile": []byte(ProvisioningDockerfile()),
		"provision.sh":         []byte(provisioningScript),
		"source-policy.json":   []byte(sourcePolicy),
	}, nil
}

// DockerfileContextFiles returns sandboxctl's fixed provisioning inputs for a user Dockerfile build.
func DockerfileContextFiles(provisioningScript string) (map[string][]byte, error) {
	if strings.TrimSpace(provisioningScript) == "" {
		return nil, errors.New("provisioning script is empty")
	}
	if len(provisioningScript) > maxProvisioningScriptSize {
		return nil, errors.New("provisioning script exceeds the size limit")
	}
	return map[string][]byte{
		"provision.Dockerfile": []byte(ProvisioningDockerfile()),
		"provision.sh":         []byte(provisioningScript),
		"source-policy.json":   []byte(sourcePolicy),
	}, nil
}

// SourcePolicyJSON returns the fixed BuildKit policy for external build inputs.
func SourcePolicyJSON() []byte {
	return []byte(sourcePolicy)
}

// ProvisioningContext renders the selected guest profile into a BuildKit context.
func ProvisioningContext(baseImage string, profile distro.Profile, guestVersion string) (map[string][]byte, error) {
	script, err := provision.Render(profile, guestVersion)
	if err != nil {
		return nil, err
	}
	return ContextFiles(baseImage, script)
}
