package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"sandboxctl/internal/client"
	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/config"
	"sandboxctl/internal/controlpb"
	"sandboxctl/internal/controlsettings"
	sshconfig "sandboxctl/internal/ssh"
)

func main() { cmdutil.Main(run) }

type options struct {
	name                  string
	guest                 uint32
	preferred             *uint32
	list, remove, replace bool
}

const usage = "usage: sandboxctl forward NAME [HOST_PORT:]GUEST_PORT [--replace-host-key] | NAME --list | NAME --remove GUEST_PORT"

func parse(arguments []string) (options, error) {
	result := options{}
	flags := flag.NewFlagSet("forward", flag.ContinueOnError)
	flags.BoolVar(&result.list, "list", false, "List saved port mappings.")
	flags.BoolVar(&result.replace, "replace-host-key", false, "Replace the forwarding host key after explicit SSH trust.")
	var remove string
	flags.StringVar(&remove, "remove", "", "Remove the mapping for this guest port.")
	// Go flags stop at the first positional argument. Collect the fixed options.
	var flagArgs, positionals []string
	for i := 0; i < len(arguments); i++ {
		argument := arguments[i]
		if strings.HasPrefix(argument, "-") {
			flagArgs = append(flagArgs, argument)
			if argument == "--remove" || argument == "-remove" {
				i++
				if i >= len(arguments) {
					return result, errors.New(usage)
				}
				flagArgs = append(flagArgs, arguments[i])
			}
		} else {
			positionals = append(positionals, argument)
		}
	}
	if err := flags.Parse(flagArgs); err != nil {
		return result, err
	}
	if len(positionals) < 1 {
		return result, errors.New(usage)
	}
	result.name = positionals[0]
	if err := cmdutil.ValidateSandboxName(result.name); err != nil {
		return result, err
	}
	result.remove = remove != ""
	if result.list || result.remove {
		if len(positionals) != 1 || (result.list && result.remove) || result.replace {
			return result, errors.New(usage)
		}
		if result.remove {
			port, err := parsePort(remove, 1)
			if err != nil {
				return result, err
			}
			result.guest = port
		}
		return result, nil
	}
	if len(positionals) != 2 {
		return result, errors.New(usage)
	}
	ports := strings.Split(positionals[1], ":")
	if len(ports) > 2 {
		return result, errors.New(usage)
	}
	guest, err := parsePort(ports[len(ports)-1], 1)
	if err != nil {
		return result, err
	}
	result.guest = guest
	if len(ports) == 2 {
		host, err := parsePort(ports[0], 1024)
		if err != nil {
			return result, err
		}
		result.preferred = &host
	}
	return result, nil
}
func parsePort(value string, min uint64) (uint32, error) {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, errors.New("port must contain decimal digits")
	}
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port < min {
		return 0, fmt.Errorf("port must range from %d to 65535", min)
	}
	return uint32(port), nil
}
func run(arguments []string) error {
	options, err := parse(arguments)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	connection, service, err := client.Dial(context.Background(), client.DefaultSocketPath)
	if err != nil {
		return err
	}
	defer connection.Close()
	if options.list {
		ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxOperationDuration)
		defer cancel()
		response, err := service.ListForwards(ctx, &controlpb.ListForwardsRequest{Name: options.name})
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "HOST\tGUEST\tSTATE\tERROR")
		for _, mapping := range response.GetForwards() {
			fmt.Fprintf(writer, "127.0.0.1:%d\t%d\t%s\t%s\n", mapping.GetHostPort(), mapping.GetGuestPort(), mapping.GetState(), mapping.GetError())
		}
		return writer.Flush()
	}
	if options.remove {
		ctx, cancel := context.WithTimeout(context.Background(), controlsettings.MaxOperationDuration)
		defer cancel()
		_, err := service.RemoveForward(ctx, &controlpb.RemoveForwardRequest{Name: options.name, GuestPort: options.guest})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "Removed port mapping for guest port %d.\n", options.guest)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prepared, err := service.PrepareForward(ctx, &controlpb.PrepareForwardRequest{Name: options.name})
	if err != nil {
		return err
	}
	paths, err := config.Resolve()
	if err != nil {
		return err
	}
	key, err := (sshconfig.Manager{Paths: paths}).InstallForwardKey(ctx, prepared.GetInstance(), prepared.GetPublicKey(), prepared.GetHostKey(), options.replace)
	if err != nil {
		return err
	}
	response, err := service.AddForward(ctx, &controlpb.AddForwardRequest{Name: options.name, InstanceId: prepared.GetInstance().GetId(), GuestPort: options.guest, PreferredHostPort: options.preferred, HostKey: key, ReplaceHostKey: options.replace})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "127.0.0.1:%d\n", response.GetForward().GetHostPort())
	return nil
}
