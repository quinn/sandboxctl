package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"sandboxctl/internal/app"
	"sandboxctl/internal/controlpb"

	"google.golang.org/grpc"
)

func TestParse(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		copy bool
		dry  bool
		fail bool
	}{
		{name: "ordinary start", args: []string{"dev1"}},
		{name: "flag after name", args: []string{"dev1", "--copy-codex-auth"}, copy: true},
		{name: "flag before name", args: []string{"--copy-codex-auth", "dev1"}, copy: true},
		{name: "single dash", args: []string{"dev1", "-copy-codex-auth"}, copy: true},
		{name: "disabled flag", args: []string{"dev1", "--copy-codex-auth=false"}},
		{name: "dry run", args: []string{"dev1", "--dry-run"}, dry: true},
		{name: "conflicting flags", args: []string{"dev1", "--dry-run", "--copy-codex-auth"}, fail: true},
		{name: "missing name", args: []string{"--copy-codex-auth"}, fail: true},
		{name: "extra name", args: []string{"dev1", "dev2"}, fail: true},
		{name: "invalid name", args: []string{"../dev1"}, fail: true},
		{name: "unknown flag", args: []string{"dev1", "--copy-auth"}, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parse(test.args)
			if test.fail {
				if err == nil {
					t.Fatal("accepted invalid start options")
				}
				return
			}
			if err != nil || got.Name != "dev1" || got.CopyCodexAuth != test.copy || got.DryRun != test.dry {
				t.Fatalf("parse = %+v, %v", got, err)
			}
		})
	}
}

type startService struct {
	controlpb.ControlServiceClient
	err      error
	response *controlpb.StartInstanceResponse
	started  bool
}

func (s *startService) StartInstance(ctx context.Context, request *controlpb.StartInstanceRequest, _ ...grpc.CallOption) (*controlpb.StartInstanceResponse, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("start has no deadline")
	}
	if request.GetName() != "dev1" {
		return nil, errors.New("unexpected sandbox name")
	}
	s.started = s.err == nil
	return s.response, s.err
}

func TestStartCopiesOnlyWhenRequested(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		service := &startService{response: &controlpb.StartInstanceResponse{Instance: &controlpb.Instance{
			Name: "dev1", SshAlias: "sandbox-dev1", GuestIp: "172.30.0.2",
		}}}
		var output bytes.Buffer
		copies := 0
		err := startSandbox(context.Background(), service, startOptions{
			StartOptions: app.StartOptions{Name: "dev1"}, CopyCodexAuth: enabled,
		}, &output, func(ctx context.Context, instance *controlpb.Instance) error {
			copies++
			if !service.started || instance.GetGuestIp() != "172.30.0.2" {
				t.Fatal("copy ran before startup or used the wrong guest")
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("copy has no deadline")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if (copies == 1) != enabled || strings.Contains(output.String(), "Copied the Codex login file.") != enabled {
			t.Fatalf("enabled = %v, copies = %d, output = %q", enabled, copies, output.String())
		}
	}
}

func TestStartFailureDoesNotCopy(t *testing.T) {
	startErr := errors.New("startup failed")
	service := &startService{err: startErr}
	var output bytes.Buffer
	err := startSandbox(context.Background(), service, startOptions{
		StartOptions: app.StartOptions{Name: "dev1"}, CopyCodexAuth: true,
	}, &output, func(context.Context, *controlpb.Instance) error {
		t.Fatal("copied after a start failure")
		return nil
	})
	if !errors.Is(err, startErr) || output.Len() != 0 {
		t.Fatalf("start = %v, output = %q", err, output.String())
	}
}

func TestCopyFailureReportsRunningSandbox(t *testing.T) {
	copyErr := errors.New("host auth.json is missing")
	service := &startService{response: &controlpb.StartInstanceResponse{Instance: &controlpb.Instance{Name: "dev1"}}}
	var output bytes.Buffer
	err := startSandbox(context.Background(), service, startOptions{
		StartOptions: app.StartOptions{Name: "dev1"}, CopyCodexAuth: true,
	}, &output, func(context.Context, *controlpb.Instance) error { return copyErr })
	if !errors.Is(err, copyErr) || !strings.Contains(err.Error(), "is running") {
		t.Fatalf("copy failure = %v", err)
	}
	if !service.started || !strings.Contains(output.String(), "Started sandbox dev1.") || strings.Contains(output.String(), "Copied") {
		t.Fatalf("started = %v, output = %q", service.started, output.String())
	}
}

func TestInvalidStartResponseDoesNotCopy(t *testing.T) {
	for _, response := range []*controlpb.StartInstanceResponse{nil, {}, {Instance: &controlpb.Instance{Name: "other"}}} {
		service := &startService{response: response}
		err := startSandbox(context.Background(), service, startOptions{
			StartOptions: app.StartOptions{Name: "dev1"}, CopyCodexAuth: true,
		}, &bytes.Buffer{}, func(context.Context, *controlpb.Instance) error {
			t.Fatal("copied after an invalid start response")
			return nil
		})
		if err == nil {
			t.Fatal("accepted an invalid start response")
		}
	}
}
