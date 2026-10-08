package hoststate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHostConfigValidatesInstallerSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.json")
	data := `{"schema_version":1,"operator":"quinn","uid_range":{"start":200000,"count":64},"gid_range":{"start":300000,"count":64},"address_pool":"172.30.0.0/16"}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadHostConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.UIDRange.Start != 200000 || config.GIDRange.Start != 300000 || config.AddressPool != "172.30.0.0/16" {
		t.Fatalf("loaded host settings = %+v", config)
	}
}

func TestLoadHostConfigRejectsSymlinkAndInvalidPool(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	valid := `{"schema_version":1,"uid_range":{"start":200000,"count":64},"gid_range":{"start":300000,"count":64},"address_pool":"172.30.0.0/16"}`
	if err := os.WriteFile(target, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHostConfig(link); err == nil {
		t.Fatal("accepted a symlink host configuration")
	}
	invalid := filepath.Join(directory, "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"schema_version":1,"uid_range":{"start":200000,"count":64},"gid_range":{"start":300000,"count":64},"address_pool":"0.0.0.0/0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHostConfig(invalid); err == nil {
		t.Fatal("accepted a public address pool")
	}
}
