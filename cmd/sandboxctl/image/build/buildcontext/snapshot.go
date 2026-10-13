// Package buildcontext prepares a private, bounded Docker build context.
package buildcontext

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
	"golang.org/x/sys/unix"
)

const (
	MaxDockerfileBytes       = 1 << 20
	MaxIgnorefileBytes       = 1 << 20
	MaxEntries               = 50000
	MaxFileBytes       int64 = 2 << 30
	MaxContextBytes    int64 = 8 << 30
	MaxArchiveBytes    int64 = 8 << 30
)

var epoch = time.Unix(0, 0)

// Snapshot contains the immutable inputs for one Dockerfile build.
type Snapshot struct {
	Archive        *os.File
	ArchiveBytes   int64
	ContextDigest  string
	Dockerfile     []byte
	DockerfileName string
	DockerfileHash string
	Directory      string
}

// BuildArg contains one explicit Dockerfile build argument.
type BuildArg struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Close closes the archive and removes its private temporary directory.
func (snapshot *Snapshot) Close() error {
	if snapshot == nil {
		return nil
	}
	var closeErr error
	if snapshot.Archive != nil {
		closeErr = snapshot.Archive.Close()
	}
	removeErr := os.RemoveAll(snapshot.Directory)
	return errors.Join(closeErr, removeErr)
}

// Options selects a context and a Dockerfile. TempDir must be private to the caller.
type Options struct {
	ContextDir string
	Dockerfile string
	TempDir    string
}

// Create snapshots accepted context files into a deterministic tar archive.
func Create(options Options) (_ *Snapshot, returnErr error) {
	if options.ContextDir == "" || options.Dockerfile == "" || options.TempDir == "" {
		return nil, errors.New("context directory, Dockerfile, and private temporary directory are required")
	}
	contextRoot, err := filepath.Abs(options.ContextDir)
	if err != nil {
		return nil, fmt.Errorf("resolve context directory path: %w", err)
	}
	contextRoot, err = filepath.EvalSymlinks(contextRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve context directory: %w", err)
	}
	rootInfo, err := os.Stat(contextRoot)
	if err != nil || !rootInfo.IsDir() {
		return nil, errors.New("context path is not a directory")
	}
	dockerPath, err := filepath.Abs(options.Dockerfile)
	if err != nil {
		return nil, fmt.Errorf("resolve Dockerfile path: %w", err)
	}
	dockerPath, err = filepath.EvalSymlinks(dockerPath)
	if err != nil {
		return nil, fmt.Errorf("resolve Dockerfile file: %w", err)
	}
	relDocker, err := filepath.Rel(contextRoot, dockerPath)
	if err != nil || relDocker == "." || relDocker == ".." || strings.HasPrefix(relDocker, ".."+string(filepath.Separator)) || filepath.IsAbs(relDocker) {
		return nil, errors.New("Dockerfile must be inside the build context")
	}
	relDocker = filepath.ToSlash(relDocker)
	rootFD, err := unix.Open(contextRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open context directory: %w", err)
	}
	defer unix.Close(rootFD)

	dockerfile, err := readRegular(rootFD, relDocker, MaxDockerfileBytes)
	if err != nil {
		return nil, fmt.Errorf("read Dockerfile: %w", err)
	}
	if len(strings.TrimSpace(string(dockerfile))) == 0 {
		return nil, errors.New("Dockerfile is empty")
	}
	ignoreName := relDocker + ".dockerignore"
	ignorePath := filepath.Join(contextRoot, filepath.FromSlash(ignoreName))
	if _, err := os.Lstat(ignorePath); errors.Is(err, os.ErrNotExist) {
		ignoreName = ".dockerignore"
	} else if err != nil {
		return nil, fmt.Errorf("inspect Dockerfile ignore file: %w", err)
	}
	var patterns []string
	ignoreBytes, err := readOptionalRegular(rootFD, ignoreName, MaxIgnorefileBytes)
	if err != nil {
		return nil, fmt.Errorf("read Dockerfile ignore file: %w", err)
	}
	if ignoreBytes != nil {
		patterns, err = ignorefile.ReadAll(strings.NewReader(string(ignoreBytes)))
		if err != nil {
			return nil, fmt.Errorf("parse Dockerfile ignore file: %w", err)
		}
	}
	matcher, err := patternmatcher.New(patterns)
	if err != nil {
		return nil, fmt.Errorf("parse Dockerfile ignore patterns: %w", err)
	}

	tempDir, err := os.MkdirTemp(options.TempDir, ".docker-context-")
	if err != nil {
		return nil, fmt.Errorf("create private context snapshot: %w", err)
	}
	if err := os.Chmod(tempDir, 0o700); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("set context snapshot permissions: %w", err)
	}
	archive, err := os.OpenFile(filepath.Join(tempDir, "context.tar"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("create context archive: %w", err)
	}
	snapshot := &Snapshot{
		Archive: archive, Dockerfile: dockerfile, DockerfileName: relDocker,
		DockerfileHash: digest(dockerfile), Directory: tempDir,
	}
	defer func() {
		if returnErr != nil {
			_ = snapshot.Close()
		}
	}()
	hasher := sha256.New()
	limited := &archiveWriter{writer: io.MultiWriter(archive, hasher), max: MaxArchiveBytes}
	writer := tar.NewWriter(limited)
	state := walkState{rootFD: rootFD, matcher: matcher, writer: writer, dockerfile: relDocker, ignore: ignoreName}
	if err := state.walkDirectory(rootFD, ""); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("collect build context: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finish context archive: %w", err)
	}
	if err := archive.Sync(); err != nil {
		return nil, fmt.Errorf("sync context archive: %w", err)
	}
	snapshot.ArchiveBytes = limited.written
	snapshot.ContextDigest = "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind context archive: %w", err)
	}
	return snapshot, nil
}

type archiveWriter struct {
	writer  io.Writer
	written int64
	max     int64
}

func (writer *archiveWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > writer.max-writer.written {
		return 0, errors.New("context archive exceeds the size limit")
	}
	n, err := writer.writer.Write(data)
	writer.written += int64(n)
	return n, err
}

type walkState struct {
	rootFD     int
	matcher    *patternmatcher.PatternMatcher
	writer     *tar.Writer
	dockerfile string
	ignore     string
	entries    int
	fileBytes  int64
}

func (state *walkState) walkDirectory(directoryFD int, prefix string) error {
	readFD, err := unix.Openat(directoryFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(readFD), prefix)
	defer file.Close()
	entries, err := file.ReadDir(-1)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." || strings.Contains(name, "/") || strings.ContainsAny(name, "\x00\r\n\t") {
			return errors.New("context contains an invalid file name")
		}
		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}
		state.entries++
		if state.entries > MaxEntries {
			return errors.New("context entry count exceeds the limit")
		}
		var stat unix.Stat_t
		if err := unix.Fstatat(directoryFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return fmt.Errorf("inspect %s: %w", rel, err)
		}
		if rel == state.dockerfile || rel == state.ignore || rel == ".dockerignore" {
			continue
		}
		excluded, err := state.matcher.MatchesOrParentMatches(rel)
		if err != nil {
			return fmt.Errorf("match ignore rules for %s: %w", rel, err)
		}
		mode := stat.Mode & 0o777
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			childFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return fmt.Errorf("open context directory %s: %w", rel, err)
			}
			header := &tar.Header{Name: rel + "/", Mode: int64(mode), Typeflag: tar.TypeDir, ModTime: epoch, AccessTime: epoch, ChangeTime: epoch, Uid: 0, Gid: 0, Format: tar.FormatPAX}
			if !excluded {
				if err := state.writer.WriteHeader(header); err != nil {
					_ = unix.Close(childFD)
					return err
				}
			}
			if err := state.walkDirectory(childFD, rel); err != nil {
				_ = unix.Close(childFD)
				return err
			}
			if err := unix.Close(childFD); err != nil {
				return err
			}
		case unix.S_IFREG:
			if excluded {
				continue
			}
			if stat.Size < 0 || stat.Size > MaxFileBytes {
				return fmt.Errorf("file %s exceeds the individual file size limit", rel)
			}
			if stat.Size > MaxContextBytes-state.fileBytes {
				return errors.New("context file bytes exceed the total size limit")
			}
			if err := state.writeRegular(directoryFD, rel, name, stat, mode); err != nil {
				return err
			}
			state.fileBytes += stat.Size
		case unix.S_IFLNK:
			if excluded {
				continue
			}
			target, err := readLink(directoryFD, name)
			if err != nil {
				return fmt.Errorf("read symbolic link %s: %w", rel, err)
			}
			if err := validateLink(rel, target); err != nil {
				return err
			}
			header := &tar.Header{Name: rel, Linkname: target, Mode: int64(mode), Typeflag: tar.TypeSymlink, ModTime: epoch, Uid: 0, Gid: 0, Format: tar.FormatPAX}
			if err := state.writer.WriteHeader(header); err != nil {
				return err
			}
		default:
			if !excluded {
				return fmt.Errorf("context entry %s has an unsupported file type", rel)
			}
		}
	}
	return nil
}

func (state *walkState) writeRegular(directoryFD int, rel, name string, before unix.Stat_t, mode uint32) error {
	fd, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open context file %s: %w", rel, err)
	}
	file := os.NewFile(uintptr(fd), rel)
	defer file.Close()
	var actual unix.Stat_t
	if err := unix.Fstat(fd, &actual); err != nil {
		return err
	}
	if !sameFile(before, actual) {
		return fmt.Errorf("context file %s changed during collection", rel)
	}
	header := &tar.Header{Name: rel, Mode: int64(mode), Size: actual.Size, Typeflag: tar.TypeReg, ModTime: epoch, AccessTime: epoch, ChangeTime: epoch, Uid: 0, Gid: 0, Format: tar.FormatPAX}
	if err := state.writer.WriteHeader(header); err != nil {
		return err
	}
	n, copyErr := io.CopyN(state.writer, file, actual.Size)
	if copyErr != nil || n != actual.Size {
		return fmt.Errorf("read context file %s: %w", rel, errors.Join(copyErr, io.ErrUnexpectedEOF))
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		return err
	}
	if !sameFile(actual, after) {
		return fmt.Errorf("context file %s changed during collection", rel)
	}
	return nil
}

func sameFile(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Size == right.Size &&
		left.Mtim == right.Mtim && left.Ctim == right.Ctim && left.Mode&unix.S_IFMT == right.Mode&unix.S_IFMT
}

func readRegular(rootFD int, name string, max int64) ([]byte, error) {
	fd, err := openAt(rootFD, name, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size < 0 || stat.Size > max {
		return nil, errors.New("file is not regular or exceeds the size limit")
	}
	data := make([]byte, stat.Size)
	if _, err := io.ReadFull(file, data); err != nil {
		return nil, err
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		return nil, err
	}
	if !sameFile(stat, after) {
		return nil, errors.New("file changed during collection")
	}
	return data, nil
}

func readOptionalRegular(rootFD int, name string, max int64) ([]byte, error) {
	data, err := readRegular(rootFD, name, max)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	return data, err
}

func openAt(rootFD int, name string, flags int) (int, error) {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return -1, errors.New("path is invalid")
	}
	parts := strings.Split(filepath.ToSlash(name), "/")
	current, err := unix.Dup(rootFD)
	if err != nil {
		return -1, err
	}
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			_ = unix.Close(current)
			return -1, errors.New("path is invalid")
		}
		openFlags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if index == len(parts)-1 {
			openFlags = flags | unix.O_CLOEXEC | unix.O_NOFOLLOW
		} else {
			openFlags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(current, part, openFlags, 0)
		_ = unix.Close(current)
		if err != nil {
			return -1, err
		}
		current = next
	}
	return current, nil
}

func readLink(dirFD int, name string) (string, error) {
	buffer := make([]byte, 4096)
	n, err := unix.Readlinkat(dirFD, name, buffer)
	if err != nil {
		return "", err
	}
	if n == len(buffer) {
		return "", errors.New("symbolic link target exceeds the size limit")
	}
	return string(buffer[:n]), nil
}

func validateLink(name, target string) error {
	if target == "" || filepath.IsAbs(target) || strings.ContainsAny(target, "\x00\r\n\t") || strings.Contains(target, "\\") {
		return fmt.Errorf("symbolic link %s has an unsafe target", name)
	}
	depth := 0
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(name))+"/"+target, "/") {
		switch part {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return fmt.Errorf("symbolic link %s escapes the context", name)
			}
		default:
			depth++
		}
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
