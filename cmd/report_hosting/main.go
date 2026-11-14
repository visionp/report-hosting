package main

import (
	"flag"
	"fmt"
	"os"

	"report_hosting/internal/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) > 0 && args[0] == "serve" {
		return serve(args[1:])
	}
	if len(args) >= 2 && args[0] == "admin" && args[1] == "create" {
		return writeAdmin("admin create", args[2:], false)
	}
	if len(args) >= 2 && args[0] == "admin" && args[1] == "set" {
		return writeAdmin("admin set", args[2:], true)
	}
	if len(args) == 0 || args[0] != "check-config" {
		return fmt.Errorf("usage: report_hosting check-config -config <file>")
	}
	f := flag.NewFlagSet("check-config", flag.ContinueOnError)
	path := f.String("config", "", "JSON configuration file")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if _, err := config.Load(*path); err != nil {
		return err
	}
	fmt.Println("configuration valid")
	return nil
}
