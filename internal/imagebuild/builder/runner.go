// Package builder coordinates one ephemeral guest image build.
package builder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"sandboxctl/internal/imagebuild/buildkit"
	"sandboxctl/internal/imagebuild/distro"
	"sandboxctl/internal/imagebuild/guest"
)

const (
	contextDirectory = "/var/tmp/sandboxctl-build/context"
	servicePath      = "/var/tmp/sandboxctl-build/image-builder.sh"
	outputPath       = "/var/tmp/sandboxctl-build/output/root.squashfs"
	maxArtifactBytes = 32 << 30
)

// VM is one isolated builder VM that owns its guest executor until it closes.
type VM interface {
	Executor() guest.GuestExecutor
	Close(context.Context) error
}

// VMFactory starts the fixed, ephemeral builder VM.
type VMFactory interface {
	Start(context.Context) (VM, error)
}

// Request contains the inputs for one guest image build.
type Request struct {
	BaseImage string
	Profile   distro.Profile
}

// Runner starts one builder VM and runs its fixed image build service.
type Runner struct {
	Factory VMFactory
}

// Build provisions a digest-pinned base image and streams its SquashFS to output.
// The caller must discard output if Build returns an error.
func (runner Runner) Build(ctx context.Context, request Request, output io.Writer) (written int64, returnErr error) {
	if runner.Factory == nil {
		return 0, errors.New("builder VM factory is unavailable")
	}
	if output == nil {
		return 0, errors.New("image output is required")
	}
	files, err := buildkit.ProvisioningContext(request.BaseImage, request.Profile)
	if err != nil {
		return 0, fmt.Errorf("create BuildKit context: %w", err)
	}
	service, err := guest.ImageBuilderService()
	if err != nil {
		return 0, err
	}
	files["image-builder.sh"] = service

	vm, err := runner.Factory.Start(ctx)
	if vm != nil {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if closeErr := vm.Close(closeCtx); closeErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close builder VM: %w", closeErr))
			}
		}()
	}
	if err != nil {
		return 0, fmt.Errorf("start builder VM: %w", err)
	}
	if vm == nil {
		return 0, errors.New("builder VM is unavailable")
	}
	executor := vm.Executor()
	if executor == nil {
		return 0, errors.New("builder VM has no guest executor")
	}
	for _, name := range []string{"Dockerfile", "provision.sh", "image-builder.sh"} {
		destination := contextDirectory + "/" + name
		if name == "image-builder.sh" {
			destination = servicePath
		}
		if err := executor.Upload(ctx, bytes.NewReader(files[name]), destination, int64(len(files[name]))); err != nil {
			return 0, fmt.Errorf("upload builder input %s: %w", name, err)
		}
	}
	result, err := executor.Run(ctx, guest.Command{
		Program: "sudo",
		Args:    []string{"-n", "/bin/sh", servicePath, "build"},
	})
	if err != nil {
		return 0, fmt.Errorf("run fixed image build service: %w%s", err, formatCommandOutput(result))
	}
	written, err = executor.Download(ctx, outputPath, output, maxArtifactBytes)
	if err != nil {
		return written, fmt.Errorf("download built SquashFS: %w", err)
	}
	if written == 0 {
		return 0, errors.New("builder returned an empty SquashFS")
	}
	return written, nil
}

const maxErrorOutputBytes = 8 << 10

func formatCommandOutput(result guest.Result) string {
	var output strings.Builder
	for _, stream := range []struct {
		name  string
		value []byte
	}{{name: "stderr", value: result.Stderr}, {name: "stdout", value: result.Stdout}} {
		value := strings.TrimSpace(string(stream.value))
		if value == "" {
			continue
		}
		truncated := len(value) > maxErrorOutputBytes
		if truncated {
			value = value[:maxErrorOutputBytes]
		}
		output.WriteString("\n")
		output.WriteString(stream.name)
		output.WriteString(": ")
		output.WriteString(value)
		if truncated {
			output.WriteString(" [truncated]")
		}
	}
	return output.String()
}
