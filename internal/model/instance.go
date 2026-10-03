package model

import (
	"time"
)

const SchemaVersion = 1

type Instance struct {
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	VCPUs         int       `json:"vcpus"`
	MemoryMiB     int       `json:"memory_mib"`
	KernelImage   string    `json:"kernel_image"`
	RootDrive     string    `json:"root_drive"`
	KernelOptions string    `json:"kernel_options"`
	TapDevice     string    `json:"tap_device"`
	GuestMAC      string    `json:"guest_mac"`
	GuestIP       string    `json:"guest_ip"`
	GatewayIP     string    `json:"gateway_ip"`
	SSHUser       string    `json:"ssh_user"`
	SSHPort       int       `json:"ssh_port"`
	HerdrLabel    string    `json:"herdr_label"`
}
