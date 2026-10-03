package names

import (
	"errors"
	"hash/fnv"
	"regexp"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func Validate(name string) error {
	if !validName.MatchString(name) {
		return errors.New("the name must start with a lower-case letter and contain only lower-case letters, digits, or hyphens")
	}
	return nil
}

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
