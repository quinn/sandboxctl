package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/user"
	"strings"

	"sandboxctl/internal/cmdutil"
	"sandboxctl/internal/hostinstall"
)

func main() {
	cmdutil.Main(run)
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("host install", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: sandboxctl host install --operator USER [OPTIONS]")
		flags.PrintDefaults()
	}
	operator := flags.String("operator", "", "Local user that will run sandboxctl.")
	uidRange := flags.String("uid-range", "200000:65536", "User ID allocation range in START:COUNT form.")
	gidRange := flags.String("gid-range", "200000:65536", "Group ID allocation range in START:COUNT form.")
	addressPool := flags.String("address-pool", "172.30.0.0/16", "Private IPv4 address pool for sandboxes.")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse host install options: %w", err)
	}
	if flags.NArg() != 0 {
		return errors.New("usage: sandboxctl host install --operator USER")
	}
	if *operator == "" {
		return errors.New("host install requires --operator USER")
	}
	uid, err := cmdutil.ParseIDRange(*uidRange)
	if err != nil {
		return fmt.Errorf("UID range: %w", err)
	}
	gid, err := cmdutil.ParseIDRange(*gidRange)
	if err != nil {
		return fmt.Errorf("GID range: %w", err)
	}
	pool, err := cmdutil.ParseAddressPool(*addressPool)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*operator) == "" || *operator == "root" || strings.HasPrefix(*operator, "-") || strings.ContainsAny(*operator, " \t\r\n") {
		return errors.New("operator must name a non-root local user")
	}
	selectedUser, err := user.Lookup(*operator)
	if err != nil {
		return fmt.Errorf("look up operator %q: %w", *operator, err)
	}
	if selectedUser.Uid == "0" {
		return errors.New("operator must not be root")
	}
	return hostinstall.Install(context.Background(), hostinstall.InstallOptions{
		Operator:    *selectedUser,
		UIDRange:    uid,
		GIDRange:    gid,
		AddressPool: pool,
	})
}
