// Package builder coordinates one ephemeral guest image build.
package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"sandboxctl/cmd/sandboxctl/image/build/buildcontext"
	"sandboxctl/cmd/sandboxctl/image/build/buildkit"
	"sandboxctl/cmd/sandboxctl/image/build/distro"
	"sandboxctl/cmd/sandboxctl/image/build/guest"
	"sandboxctl/cmd/sandboxctl/image/build/provision"
)

const (
	contextDirectory      = "/var/tmp/sandboxctl-build/context"
	archivePath           = "/var/tmp/sandboxctl-build/context.tar"
	userDockerfile        = "/var/tmp/sandboxctl-build/input/Dockerfile"
	settingsPath          = "/var/tmp/sandboxctl-build/settings.json"
	servicePath           = "/var/tmp/sandboxctl-build/image-builder.sh"
	outputPath            = "/var/tmp/sandboxctl-build/output/root.squashfs"
	maxArtifactBytes      = 32 << 30
	maxBuildSettingsBytes = 64 << 10
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var buildArgNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var targetPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

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
	BaseImage    string
	Profile      distro.Profile
	GuestVersion string
	Dockerfile   *DockerfileInput
}

// DockerfileInput contains one snapshotted user build context and its settings.
type DockerfileInput struct {
	Archive        io.Reader
	ArchiveBytes   int64
	Dockerfile     []byte
	DockerfileName string
	ContextDigest  string
	DockerfileHash string
	Target         string
	BuildArgs      []buildcontext.BuildArg
}

// Runner starts one builder VM and runs its fixed image build service.
type Runner struct {
	Factory VMFactory
}

// Build provisions a digest-pinned base image and streams its SquashFS to output.
// The caller must discard output if Build returns an error.
func (runner Runner) Build(ctx context.Context, request Request, output io.Writer) (written int64, returnErr error) {
	startedAt := time.Now()
	if runner.Factory == nil {
		return 0, errors.New("builder VM factory is unavailable")
	}
	if output == nil {
		return 0, errors.New("image output is required")
	}
	var files map[string][]byte
	var err error
	operation := "build-base"
	if request.Dockerfile == nil {
		if request.BaseImage == "" {
			return 0, errors.New("base image or Dockerfile input is required")
		}
		files, err = buildkit.ProvisioningContext(request.BaseImage, request.Profile, request.GuestVersion)
	} else {
		if request.BaseImage != "" || validateDockerfileInput(request.Dockerfile) != nil {
			return 0, errors.New("Dockerfile build input is invalid")
		}
		operation = "build-dockerfile"
		var script string
		script, err = provision.Render(request.Profile, request.GuestVersion)
		if err == nil {
			files, err = buildkit.DockerfileContextFiles(script)
		}
	}
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
			cleanupStarted := time.Now()
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if closeErr := vm.Close(closeCtx); closeErr != nil {
				slog.Error("builder VM cleanup failed", "error", closeErr, "duration", time.Since(cleanupStarted))
				returnErr = errors.Join(returnErr, fmt.Errorf("close builder VM: %w", closeErr))
			} else {
				slog.Info("builder VM cleanup completed", "duration", time.Since(cleanupStarted))
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
	for _, name := range []string{"Dockerfile", "provision.Dockerfile", "provision.sh", "source-policy.json"} {
		if _, ok := files[name]; !ok {
			continue
		}
		destination := contextDirectory + "/" + name
		if err := executor.Upload(ctx, bytes.NewReader(files[name]), destination, int64(len(files[name]))); err != nil {
			return 0, fmt.Errorf("upload builder input %s: %w", name, err)
		}
	}
	if input := request.Dockerfile; input != nil {
		if err := executor.Upload(ctx, bytes.NewReader(input.Dockerfile), userDockerfile, int64(len(input.Dockerfile))); err != nil {
			return 0, fmt.Errorf("upload user Dockerfile: %w", err)
		}
		if err := executor.Upload(ctx, input.Archive, archivePath, input.ArchiveBytes); err != nil {
			return 0, fmt.Errorf("upload user build context: %w", err)
		}
		args := input.BuildArgs
		if args == nil {
			args = []buildcontext.BuildArg{}
		}
		settings, err := json.Marshal(struct {
			DockerfileName string                  `json:"dockerfile_name"`
			ContextDigest  string                  `json:"context_digest"`
			DockerfileHash string                  `json:"dockerfile_hash"`
			Target         string                  `json:"target,omitempty"`
			BuildArgs      []buildcontext.BuildArg `json:"build_args"`
		}{input.DockerfileName, input.ContextDigest, input.DockerfileHash, input.Target, args})
		if err != nil {
			return 0, fmt.Errorf("encode build settings: %w", err)
		}
		if len(settings) > maxBuildSettingsBytes {
			return 0, errors.New("Dockerfile build settings exceed the size limit")
		}
		if err := executor.Upload(ctx, bytes.NewReader(settings), settingsPath, int64(len(settings))); err != nil {
			return 0, fmt.Errorf("upload build settings: %w", err)
		}
	}
	slog.Info("builder inputs uploaded", "mode", operation, "context_archive_bytes", archiveBytes(request.Dockerfile),
		"dockerfile_bytes", dockerfileBytes(request.Dockerfile), "target", targetName(request.Dockerfile),
		"build_arg_count", buildArgCount(request.Dockerfile))
	if err := executor.Upload(ctx, bytes.NewReader(files["image-builder.sh"]), servicePath, int64(len(files["image-builder.sh"]))); err != nil {
		return 0, fmt.Errorf("upload builder service: %w", err)
	}
	serviceStarted := time.Now()
	slog.Info("builder image build service started", "mode", operation)
	result, err := executor.Run(ctx, guest.Command{
		Program: "sudo",
		Args:    []string{"-n", "/bin/sh", servicePath, operation},
	})
	if err != nil {
		slog.Error("builder image build service failed", "mode", operation, "duration", time.Since(serviceStarted), "error", err)
		return 0, fmt.Errorf("run fixed image build service: %w%s", err, formatCommandOutput(result))
	}
	slog.Info("builder image build service completed", "mode", operation, "duration", time.Since(serviceStarted))
	written, err = executor.Download(ctx, outputPath, output, maxArtifactBytes)
	if err != nil {
		return written, fmt.Errorf("download built SquashFS: %w", err)
	}
	if written == 0 {
		return 0, errors.New("builder returned an empty SquashFS")
	}
	slog.Info("builder SquashFS downloaded", "bytes", written, "duration", time.Since(startedAt))
	return written, nil
}

func archiveBytes(input *DockerfileInput) int64 {
	if input == nil {
		return 0
	}
	return input.ArchiveBytes
}

func dockerfileBytes(input *DockerfileInput) int {
	if input == nil {
		return 0
	}
	return len(input.Dockerfile)
}

func targetName(input *DockerfileInput) string {
	if input == nil {
		return ""
	}
	return input.Target
}

func buildArgCount(input *DockerfileInput) int {
	if input == nil {
		return 0
	}
	return len(input.BuildArgs)
}

func validateDockerfileInput(input *DockerfileInput) error {
	if input == nil || input.Archive == nil || input.ArchiveBytes < 1 || input.ArchiveBytes > buildcontext.MaxArchiveBytes ||
		len(input.Dockerfile) < 1 || len(input.Dockerfile) > buildcontext.MaxDockerfileBytes ||
		!digestPattern.MatchString(input.ContextDigest) || !digestPattern.MatchString(input.DockerfileHash) ||
		input.DockerfileName == "" || strings.HasPrefix(input.DockerfileName, "/") || strings.ContainsAny(input.DockerfileName, "\\\x00\r\n\t") {
		return errors.New("Dockerfile build input identity is invalid")
	}
	for _, part := range strings.Split(input.DockerfileName, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("Dockerfile build input path is invalid")
		}
	}
	if input.Target != "" && !targetPattern.MatchString(input.Target) {
		return errors.New("Dockerfile build target is invalid")
	}
	if len(input.BuildArgs) > 128 {
		return errors.New("Dockerfile build argument count exceeds the limit")
	}
	seen := make(map[string]bool, len(input.BuildArgs))
	total := 0
	for _, argument := range input.BuildArgs {
		if !buildArgNamePattern.MatchString(argument.Name) || strings.ContainsRune(argument.Value, 0) || seen[argument.Name] {
			return errors.New("Dockerfile build arguments are invalid")
		}
		seen[argument.Name] = true
		total += len(argument.Name) + len(argument.Value)
		if total > maxBuildSettingsBytes {
			return errors.New("Dockerfile build arguments exceed the size limit")
		}
	}
	return nil
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
			marker := "[earlier output truncated]\n"
			remaining := maxErrorOutputBytes - len(marker)
			value = marker + value[len(value)-remaining:]
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
