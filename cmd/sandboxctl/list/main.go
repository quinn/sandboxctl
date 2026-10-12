package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	jsonOutput := len(arguments) == 1 && arguments[0] == "--json"
	if len(arguments) > 1 || (len(arguments) == 1 && !jsonOutput) {
		return errors.New("usage: sandboxctl list [--json]")
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()

	ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxOperationDuration)
	defer cancel()
	response, err := service.ListInstances(ctx, &controlpb.ListInstancesRequest{})
	if err != nil {
		return fmt.Errorf("list sandbox instances: %w", err)
	}
	if len(response.GetInstances()) == 0 {
		if jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(listJSON{SchemaVersion: 1, Instances: []listInstanceJSON{}})
		}
		fmt.Fprintln(os.Stdout, "No sandboxes exist.")
		return nil
	}
	if jsonOutput {
		output := listJSON{SchemaVersion: 1, Instances: make([]listInstanceJSON, 0, len(response.GetInstances()))}
		for _, instance := range response.GetInstances() {
			output.Instances = append(output.Instances, listInstanceJSON{
				Name: instance.GetName(), State: instance.GetRuntimeState(), GuestIP: instance.GetGuestIp(),
				SSHAlias: instance.GetSshAlias(), ImageVersion: instance.GetImageVersion(),
				VCPUs: instance.GetVcpus(), MemoryMiB: instance.GetMemoryMib(),
				StateDiskBytes: instance.GetStateDiskBytes(),
			})
		}
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	fmt.Fprintln(os.Stdout, "NAME\tSTATE\tADDRESS\tSSH ALIAS\tIMAGE VERSION\tCPUS\tMEMORY\tSTATE DISK")
	for _, instance := range response.GetInstances() {
		diskGiB := float64(instance.GetStateDiskBytes()) / (1024 * 1024 * 1024)
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\t%d\t%d MiB\t%.1f GiB\n",
			instance.GetName(), instance.GetRuntimeState(), instance.GetGuestIp(), instance.GetSshAlias(),
			instance.GetImageVersion(), instance.GetVcpus(), instance.GetMemoryMib(), diskGiB)
	}
	return nil
}

type listJSON struct {
	SchemaVersion int                `json:"schema_version"`
	Instances     []listInstanceJSON `json:"instances"`
}

type listInstanceJSON struct {
	Name           string `json:"name"`
	State          string `json:"state"`
	GuestIP        string `json:"guest_ip"`
	SSHAlias       string `json:"ssh_alias"`
	ImageVersion   string `json:"image_version"`
	VCPUs          int32  `json:"vcpus"`
	MemoryMiB      int32  `json:"memory_mib"`
	StateDiskBytes int64  `json:"state_disk_bytes"`
}
