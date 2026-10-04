package names

import (
	"hash/fnv"
)

// NetworkIdentity returns stable TAP, MAC, and host-address values for name.
func NetworkIdentity(name string) (tap string, mac string, hostPart byte) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	value := h.Sum32()
	hostPart = byte(10 + value%220)
	tap = "fc-" + name
	if len(tap) > 15 {
		tap = tap[:15]
	}
	mac = "06:00:ac:10:" + hexByte(byte(value>>8)) + ":" + hexByte(byte(value))
	return tap, mac, hostPart
}

func hexByte(value byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[value>>4], digits[value&0x0f]})
}
