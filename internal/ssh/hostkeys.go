package ssh

import (
	"errors"
	"net"

	"sandboxctl/internal/forward"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// forwardHostKeyAlgorithms gets host-key preferences from managed trust data.
// The handshake must still verify the server's actual key.
func (m Manager) forwardHostKeyAlgorithms(alias, address string) ([]string, error) {
	callback, err := knownhosts.New(m.Paths.KnownHosts)
	if err != nil {
		return nil, err
	}
	remote, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}
	// The probe cannot match a real key. KeyError lists the keys for this host,
	// with hashed names and host patterns resolved by the knownhosts package.
	err = callback(net.JoinHostPort(alias, "22"), remote, hostKeyProbe{})
	var keys *knownhosts.KeyError
	if !errors.As(err, &keys) {
		return nil, err
	}
	trusted := make([]gossh.PublicKey, 0, len(keys.Want))
	for _, known := range keys.Want {
		trusted = append(trusted, known.Key)
	}
	return forward.HostKeyAlgorithms(trusted...), nil
}

type hostKeyProbe struct{}

func (hostKeyProbe) Type() string    { return "sandboxctl-host-key-probe" }
func (hostKeyProbe) Marshal() []byte { return nil }
func (hostKeyProbe) Verify([]byte, *gossh.Signature) error {
	return errors.New("host-key probe cannot verify signatures")
}
