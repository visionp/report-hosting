package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
	"report_hosting/internal/config"
	"report_hosting/internal/domain"
	"report_hosting/internal/repository"
	"report_hosting/internal/service"
	"report_hosting/internal/storage"
)

// writeAdmin creates the administrator, or with replace=true swaps the existing
// one (new username and password, all sessions ended, change audited).
func writeAdmin(name string, args []string, replace bool) error {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	path := f.String("config", "", "JSON configuration file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("usage: report_hosting %s -config <file> <username>", name)
	}
	username := f.Arg(0)
	if err := domain.ValidateUsername(username); err != nil {
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := (storage.Layout{Root: c.DataDir}).Init(); err != nil {
		return err
	}
	var password []byte
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Password: ")
		password, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
	} else {
		password, err = io.ReadAll(io.LimitReader(os.Stdin, 4097))
		password = []byte(strings.TrimRight(string(password), "\r\n"))
	}
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	hash, err := service.HashPassword(string(password), c.Argon2)
	for i := range password {
		password[i] = 0
	}
	if err != nil {
		return err
	}
	st, err := repository.Open(filepath.Join(c.DataDir, "db", "report_hosting.sqlite"))
	if err != nil {
		return err
	}
	defer st.Close()
	if replace {
		if err = st.ReplaceAdmin(context.Background(), username, hash); err != nil {
			return err
		}
		fmt.Println("administrator replaced; all sessions ended")
		return nil
	}
	if err = st.CreateAdmin(context.Background(), username, hash); err != nil {
		return err
	}
	fmt.Println("administrator created")
	return nil
}
