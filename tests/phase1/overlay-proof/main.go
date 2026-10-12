package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"sandboxctl/tests/phase1/overlayboot"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "overlay-proof:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("overlay-proof", flag.ContinueOnError)
	kernel := flags.String("kernel", "", "Path to the Firecracker kernel.")
	initramfs := flags.String("initramfs", "", "Path to the overlay initramfs.")
	squashfs := flags.String("squashfs", "", "Path to the read-only SquashFS image.")
	stateDisk := flags.String("state-disk", "", "Path to the writable ext4 state disk.")
	firecrackerPath := flags.String("firecracker", "firecracker", "Path to the Firecracker executable.")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	paths := []*string{kernel, initramfs, squashfs, stateDisk}
	labels := []string{"kernel", "initramfs", "SquashFS image", "state disk"}
	for i, path := range paths {
		if *path == "" {
			return fmt.Errorf("%s path is required", labels[i])
		}
		info, err := os.Stat(*path)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", labels[i], err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a regular file", labels[i])
		}
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path = absolute
	}
	baseDigest, err := fileSHA256(*squashfs)
	if err != nil {
		return fmt.Errorf("hash SquashFS image: %w", err)
	}
	lock, err := os.OpenFile(*stateDisk+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open state disk lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("state disk is already in use: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	resolved, err := exec.LookPath(*firecrackerPath)
	if err != nil {
		return fmt.Errorf("find Firecracker: %w", err)
	}
	workDir := filepath.Join("tmp", "config", "runtime")
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return fmt.Errorf("create proof runtime directory: %w", err)
	}
	socket := filepath.Join(workDir, "overlay-proof.sock")
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refuse to remove non-socket path %s", socket)
		}
		if err := os.Remove(socket); err != nil {
			return fmt.Errorf("remove stale API socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	logPath := filepath.Join(workDir, "firecracker.log")
	defer os.Remove(socket)
	cfg := overlayboot.Configuration(*kernel, *initramfs, *squashfs, *stateDisk, socket, logPath)
	ctx := context.Background()
	process := firecracker.VMCommandBuilder{}.WithBin(resolved).WithSocketPath(socket).WithStdin(os.Stdin).WithStdout(os.Stdout).WithStderr(os.Stderr).Build(ctx)
	machine, err := firecracker.NewMachine(ctx, cfg, firecracker.WithProcessRunner(process))
	if err != nil {
		return fmt.Errorf("create Firecracker machine: %w", err)
	}
	if err := machine.Start(ctx); err != nil {
		return fmt.Errorf("start overlay proof VM: %w", err)
	}
	fmt.Fprintln(os.Stderr, "Overlay proof VM started. Use the serial console to test file persistence. Press Ctrl-C to stop it.")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	finished := make(chan error, 1)
	go func() { finished <- machine.Wait(ctx) }()
	select {
	case <-signals:
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		shutdownErr := machine.Shutdown(stopCtx)
		cancel()
		if shutdownErr != nil {
			if err := machine.StopVMM(); err != nil {
				return fmt.Errorf("request guest shutdown: %v; stop Firecracker: %w", shutdownErr, err)
			}
		} else {
			select {
			case waitErr := <-finished:
				if err := verifyBaseDigest(baseDigest, *squashfs); err != nil {
					return err
				}
				return waitErr
			case <-time.After(15 * time.Second):
				if err := machine.StopVMM(); err != nil {
					return fmt.Errorf("stop Firecracker after guest shutdown timed out: %w", err)
				}
			}
		}
		select {
		case waitErr := <-finished:
			if err := verifyBaseDigest(baseDigest, *squashfs); err != nil {
				return err
			}
			return waitErr
		case <-time.After(15 * time.Second):
			return errors.New("Firecracker did not stop after the stop request")
		}
	case err := <-finished:
		if verifyErr := verifyBaseDigest(baseDigest, *squashfs); verifyErr != nil {
			return verifyErr
		}
		return err
	}
}

func verifyBaseDigest(before, path string) error {
	after, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("hash SquashFS image after the VM stopped: %w", err)
	}
	if after != before {
		return errors.New("the SquashFS image changed during the proof")
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
