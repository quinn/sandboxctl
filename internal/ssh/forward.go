package ssh

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/forward"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// InstallForwardKey verifies the instance host key and installs a restricted
// forwarding public key without sending the operator private key to sandboxd.
func (m Manager) InstallForwardKey(ctx context.Context, instance *controlpb.Instance, publicKey, pinned string, replace bool) (string, error) {
	if instance == nil || !instanceID.MatchString(instance.GetId()) {
		return "", errors.New("invalid sandbox identity")
	}
	if err := m.PrepareKey(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(m.Paths.PrivateKey())
	if err != nil {
		return "", err
	}
	signer, err := gossh.ParsePrivateKey(data)
	if err != nil {
		return "", fmt.Errorf("read operator SSH key: %w", err)
	}
	key, _, options, rest, err := gossh.ParseAuthorizedKey([]byte(publicKey))
	if err != nil || key.Type() != gossh.KeyAlgoED25519 || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 {
		return "", errors.New("invalid forwarding public key")
	}
	canonical := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
	alias := "sandbox-instance-" + instance.GetId()
	var verified string
	callback := func(_ string, address net.Addr, key gossh.PublicKey) error {
		if err := m.verifyForwardHost(alias, address, key); err != nil {
			return err
		}
		verified = strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
		if pinned != "" && pinned != verified && !replace {
			return errors.New("forwarding host key changed; verify it and use --replace-host-key")
		}
		return nil
	}
	client, err := forward.Dial(ctx, net.JoinHostPort(instance.GetGuestIp(), "22"), &gossh.ClientConfig{User: instance.GetSshUser(), Auth: []gossh.AuthMethod{gossh.PublicKeys(signer)}, HostKeyCallback: callback, HostKeyAlgorithms: []string{gossh.KeyAlgoED25519}, Timeout: 2 * time.Minute})
	if err != nil {
		return "", fmt.Errorf("connect to sandbox for forwarding setup: %w", err)
	}
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	session.Stdin = strings.NewReader(forwardKeyScript(canonical))
	session.Stdout = io.Discard
	session.Stderr = &boundedOutput{writer: os.Stderr, left: 4096}
	if err = session.Run("sh -s"); err != nil {
		return "", fmt.Errorf("install guest forwarding key: %w", err)
	}
	return verified, nil
}

type boundedOutput struct {
	writer io.Writer
	left   int
}

func (w *boundedOutput) Write(data []byte) (int, error) {
	size := len(data)
	if len(data) > w.left {
		data = data[:w.left]
	}
	w.left -= len(data)
	_, err := w.writer.Write(data)
	return size, err
}

func (m Manager) verifyForwardHost(alias string, address net.Addr, key gossh.PublicKey) error {
	fd, err := syscall.Open(m.Paths.KnownHosts, syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), m.Paths.KnownHosts)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || int(stat.Uid) != os.Geteuid() || info.Mode().Perm() != 0600 {
		return errors.New("managed known-hosts file must be private and owned by the operator")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	callback, err := knownhosts.New(m.Paths.KnownHosts)
	if err != nil {
		return err
	}
	err = callback(net.JoinHostPort(alias, "22"), address, key)
	if err == nil {
		return nil
	}
	var unknown *knownhosts.KeyError
	if !errors.As(err, &unknown) || len(unknown.Want) != 0 {
		return fmt.Errorf("SSH host key check failed for %s: %w; verify the key before changing %s", alias, err, m.Paths.KnownHosts)
	}
	fmt.Fprintf(os.Stderr, "Trust SSH host key for %s (%s)? [y/N] ", alias, gossh.FingerprintSHA256(key))
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return errors.New("SSH host key trust requires an interactive confirmation")
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		return errors.New("SSH host key was not trusted")
	}
	if info.Size() > 0 {
		var last [1]byte
		if _, err = file.ReadAt(last[:], info.Size()-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			if _, err = file.Seek(0, io.SeekEnd); err != nil {
				return err
			}
			if _, err = file.Write([]byte("\n")); err != nil {
				return err
			}
		}
	}
	if _, err = file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err = fmt.Fprintln(file, knownhosts.Line([]string{alias}, key)); err != nil {
		return err
	}
	return file.Sync()
}

func forwardKeyScript(key string) string {
	// The key is parsed and encoded before substitution. It cannot contain quotes.
	return `set -eu
umask 077
mkdir -p "$HOME/.ssh"
test ! -L "$HOME/.ssh"
chmod 0700 "$HOME/.ssh"
test ! -L "$HOME/.ssh/sandboxctl_forward_keys"
key_file=$(mktemp "$HOME/.ssh/.forward-keys.XXXXXX")
trap 'rm -f "$key_file"' EXIT
printf '%s\n' 'restrict,port-forwarding,command="/usr/bin/false",permitopen="127.0.0.1:*" ` + key + `' >"$key_file"
chmod 0600 "$key_file"
mv -f "$key_file" "$HOME/.ssh/sandboxctl_forward_keys"
sudo -n mkdir -p /etc/ssh/sshd_config.d
printf '%s\n' 'AuthorizedKeysFile .ssh/authorized_keys .ssh/sandboxctl_forward_keys' 'AllowTcpForwarding local' | sudo -n tee /etc/ssh/sshd_config.d/00-sandboxctl-forwarding.conf >/dev/null
sudo -n chmod 0600 /etc/ssh/sshd_config.d/00-sandboxctl-forwarding.conf
sudo -n /usr/sbin/sshd -t
sudo -n /usr/sbin/sshd -T | grep -Fx 'authorizedkeysfile .ssh/authorized_keys .ssh/sandboxctl_forward_keys' >/dev/null
sudo -n /usr/sbin/sshd -T | grep -Fx 'allowtcpforwarding local' >/dev/null
sudo -n systemctl reload ssh.service
`
}
