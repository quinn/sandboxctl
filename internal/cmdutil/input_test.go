package cmdutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateSandboxName(t *testing.T) {
	for _, name := range []string{"dev", "dev-1", "a0123456789012345678901234567890"} {
		if err := ValidateSandboxName(name); err != nil {
			t.Errorf("ValidateSandboxName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "1dev", "Dev", "dev_name", "a01234567890123456789012345678901"} {
		if err := ValidateSandboxName(name); err == nil {
			t.Errorf("ValidateSandboxName(%q) accepted invalid name", name)
		}
	}
}

func TestValidateGuestUser(t *testing.T) {
	for _, name := range []string{"dev", "_guest", "dev-user1"} {
		if err := ValidateGuestUser(name); err != nil {
			t.Errorf("ValidateGuestUser(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "1dev", "Dev", "dev.user", "dev user"} {
		if err := ValidateGuestUser(name); err == nil {
			t.Errorf("ValidateGuestUser(%q) accepted invalid name", name)
		}
	}
}

func TestValidateImageVersion(t *testing.T) {
	valid := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := ValidateImageVersion(valid); err != nil {
		t.Fatalf("ValidateImageVersion(%q): %v", valid, err)
	}
	for _, value := range []string{"", "../../tmp", "sha256:ABCDEF", "sha256:" + strings.Repeat("a", 63)} {
		if err := ValidateImageVersion(value); err == nil {
			t.Errorf("ValidateImageVersion(%q) accepted invalid input", value)
		}
	}
}

func TestParseIDRange(t *testing.T) {
	for _, test := range []struct {
		value string
		want  IDRange
		bad   bool
	}{
		{value: "200000:65536", want: IDRange{Start: 200000, Count: 65536}},
		{value: "65536:1", want: IDRange{Start: 65536, Count: 1}},
		{value: "200000", bad: true},
		{value: "1000:10", bad: true},
		{value: "200000:0", bad: true},
		{value: "2147483647:2", bad: true},
	} {
		got, err := ParseIDRange(test.value)
		if (err != nil) != test.bad {
			t.Errorf("ParseIDRange(%q) error = %v", test.value, err)
		}
		if err == nil && got != test.want {
			t.Errorf("ParseIDRange(%q) = %+v, want %+v", test.value, got, test.want)
		}
	}
}

func TestParseAddressPool(t *testing.T) {
	for _, test := range []struct {
		value string
		bad   bool
	}{
		{value: "172.30.0.0/16"},
		{value: "10.2.0.0/16"},
		{value: "172.30.0.1/16", bad: true},
		{value: "172.30.0.0/24", bad: true},
		{value: "2001:db8::/16", bad: true},
		{value: "172.30.0.0/16x", bad: true},
	} {
		_, err := ParseAddressPool(test.value)
		if (err != nil) != test.bad {
			t.Errorf("ParseAddressPool(%q) error = %v", test.value, err)
		}
	}
}

func TestPositionalArgumentLast(t *testing.T) {
	for _, test := range []struct {
		input []string
		want  []string
	}{
		{input: []string{"dev1", "--cpus", "4"}, want: []string{"--cpus", "4", "dev1"}},
		{input: []string{"--cpus", "4", "dev1"}, want: []string{"--cpus", "4", "dev1"}},
		{input: []string{}, want: []string{}},
	} {
		if got := PositionalArgumentLast(test.input); !reflect.DeepEqual(got, test.want) {
			t.Errorf("PositionalArgumentLast(%v) = %v, want %v", test.input, got, test.want)
		}
	}
}
