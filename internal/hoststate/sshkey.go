package hoststate

import (
	"encoding/base64"
	"encoding/binary"
)

func validEd25519KeyData(encoded string) bool {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) != 51 {
		return false
	}
	if binary.BigEndian.Uint32(data[:4]) != 11 || string(data[4:15]) != "ssh-ed25519" {
		return false
	}
	return binary.BigEndian.Uint32(data[15:19]) == 32
}
