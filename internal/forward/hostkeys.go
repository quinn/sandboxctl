package forward

import "golang.org/x/crypto/ssh"

// HostKeyAlgorithms uses OpenSSH's default host-key order for algorithms that
// Go supports. Algorithms for trusted keys come first, in the same order.
func HostKeyAlgorithms(trusted ...ssh.PublicKey) []string {
	// OpenSSH's default HostKeyAlgorithms list, filtered below for this Go library.
	// https://man.openbsd.org/ssh_config#HostKeyAlgorithms
	order := []string{
		ssh.CertAlgoED25519v01,
		ssh.CertAlgoECDSA256v01,
		ssh.CertAlgoECDSA384v01,
		ssh.CertAlgoECDSA521v01,
		ssh.CertAlgoSKED25519v01,
		ssh.CertAlgoSKECDSA256v01,
		"webauthn-sk-ecdsa-sha2-nistp256-cert-v01@openssh.com",
		ssh.CertAlgoRSASHA512v01,
		ssh.CertAlgoRSASHA256v01,
		"ssh-mldsa44-ed25519-cert-v01@openssh.com",
		ssh.KeyAlgoED25519,
		ssh.KeyAlgoECDSA256,
		ssh.KeyAlgoECDSA384,
		ssh.KeyAlgoECDSA521,
		ssh.KeyAlgoSKED25519,
		"webauthn-sk-ecdsa-sha2-nistp256@openssh.com",
		ssh.KeyAlgoSKECDSA256,
		ssh.KeyAlgoRSASHA512,
		ssh.KeyAlgoRSASHA256,
		"ssh-mldsa44-ed25519@openssh.com",
	}
	supported := make(map[string]bool)
	for _, algorithm := range ssh.SupportedAlgorithms().HostKeys {
		supported[algorithm] = true
	}
	preferred := make(map[string]bool)
	for _, key := range trusted {
		if key == nil {
			continue
		}
		switch key.Type() {
		case ssh.KeyAlgoRSA:
			preferred[ssh.KeyAlgoRSASHA512] = true
			preferred[ssh.KeyAlgoRSASHA256] = true
		case ssh.CertAlgoRSAv01:
			preferred[ssh.CertAlgoRSASHA512v01] = true
			preferred[ssh.CertAlgoRSASHA256v01] = true
		default:
			preferred[key.Type()] = true
		}
	}
	result := make([]string, 0, len(order))
	for _, known := range []bool{true, false} {
		for _, algorithm := range order {
			if supported[algorithm] && preferred[algorithm] == known {
				result = append(result, algorithm)
			}
		}
	}
	return result
}
