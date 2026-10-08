package cmdutil

import (
	"errors"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// IDRange defines a contiguous range of numeric user or group IDs.
type IDRange struct {
	Start uint32 `json:"start"`
	Count uint32 `json:"count"`
}

var (
	validSandboxName  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	validGuestUser    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	validImageVersion = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ValidateSandboxName reports whether value is a valid sandbox name.
func ValidateSandboxName(value string) error {
	if !validSandboxName.MatchString(value) {
		return errors.New("the name must start with a lower-case letter and contain only lower-case letters, digits, or hyphens")
	}
	return nil
}

// ValidateGuestUser reports whether value is a valid Linux guest user name.
func ValidateGuestUser(value string) error {
	if !validGuestUser.MatchString(value) {
		return errors.New("SSH user must start with a lower-case letter or underscore and contain only lower-case letters, digits, underscores, or hyphens")
	}
	return nil
}

// ValidateImageVersion checks a pinned image version before path construction.
func ValidateImageVersion(value string) error {
	if !validImageVersion.MatchString(value) {
		return errors.New("image version must use sha256: followed by 64 lower-case hexadecimal characters")
	}
	return nil
}

// ParseIDRange parses a START:COUNT identifier range.
func ParseIDRange(value string) (IDRange, error) {
	startText, countText, ok := strings.Cut(value, ":")
	if !ok {
		return IDRange{}, errors.New("use START:COUNT")
	}
	start, err := strconv.ParseUint(startText, 10, 32)
	if err != nil || start < 65536 {
		return IDRange{}, errors.New("start must be a number at least 65536")
	}
	count, err := strconv.ParseUint(countText, 10, 32)
	if err != nil || count == 0 || start+count-1 > 1<<31-1 {
		return IDRange{}, errors.New("count must be positive and range must fit in a signed 32-bit identifier")
	}
	return IDRange{Start: uint32(start), Count: uint32(count)}, nil
}

// ParseAddressPool parses a private IPv4 /16 address pool.
func ParseAddressPool(value string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || prefix.Bits() != 16 || prefix.Masked() != prefix {
		return netip.Prefix{}, errors.New("address pool must be a private IPv4 /16 network")
	}
	return prefix, nil
}

// PositionalArgumentLast moves a leading positional argument after the options.
func PositionalArgumentLast(arguments []string) []string {
	if len(arguments) > 0 && !strings.HasPrefix(arguments[0], "-") {
		return append(append([]string{}, arguments[1:]...), arguments[0])
	}
	return arguments
}
