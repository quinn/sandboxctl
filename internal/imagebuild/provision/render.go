// Copyright 2023 FoundryLabs, Inc.
// Derived from the E2B runtime provisioning renderer and modified for sandboxctl.
// See third_party/licenses/e2b-apache-2.0.txt.
//
// Package provision renders a sandboxctl-owned guest provisioning script.
package provision

import (
	"bytes"
	"embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"sandboxctl/internal/imagebuild/distro"
)

//go:embed provision.sh
var files embed.FS

var guestVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// Render creates a deterministic provisioning script for a supported profile.
func Render(profile distro.Profile, guestVersion string) (string, error) {
	canonical, err := distro.ForID(profile.ID)
	if err != nil {
		return "", err
	}
	if !guestVersionPattern.MatchString(guestVersion) {
		return "", fmt.Errorf("guest version %q is invalid", guestVersion)
	}
	data := struct {
		ID           string
		GuestVersion string
		Packages     string
		PackageQuery string
		PackageSetup string
		InitBinary   string
		SSHService   string
		AdminGroup   string
		CABundle     string
		CARefresh    string
		BootSetup    []string
	}{
		ID:           canonical.ID,
		GuestVersion: guestVersion,
		Packages:     strings.Join(canonical.Packages, " "),
		PackageQuery: canonical.PackageQuery,
		PackageSetup: canonical.PackageInstall,
		InitBinary:   canonical.InitBinary,
		SSHService:   canonical.SSHService,
		AdminGroup:   canonical.AdminGroup,
		CABundle:     canonical.CABundle,
		CARefresh:    canonical.CARefresh,
	}
	data.BootSetup, err = distro.BootSetup(canonical)
	if err != nil {
		return "", err
	}

	content, err := files.ReadFile("provision.sh")
	if err != nil {
		return "", fmt.Errorf("read embedded provisioning script: %w", err)
	}
	t, err := template.New("provision.sh").Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parse provisioning script: %w", err)
	}
	var output bytes.Buffer
	if err := t.Execute(&output, data); err != nil {
		return "", fmt.Errorf("render provisioning script: %w", err)
	}
	return output.String(), nil
}
