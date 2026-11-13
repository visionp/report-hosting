package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/term"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"report_hosting/internal/config"
	"report_hosting/internal/domain"
	"report_hosting/internal/handler"
	"report_hosting/internal/repository"
	"report_hosting/internal/routing"
	"report_hosting/internal/service"
	"report_hosting/internal/storage"
	"strings"
	"syscall"
	"time"
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
		return createAdmin(args[2:])
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

func createAdmin(args []string) error {
	f := flag.NewFlagSet("admin create", flag.ContinueOnError)
	path := f.String("config", "", "JSON configuration file")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("usage: report_hosting admin create -config <file> <username>")
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
	if err = st.CreateAdmin(context.Background(), username, hash); err != nil {
		return err
	}
	fmt.Println("administrator created")
	return nil
}

func serve(args []string) error {
	f := flag.NewFlagSet("serve", flag.ContinueOnError)
	path := f.String("config", "", "JSON configuration file")
	if err := f.Parse(args); err != nil {
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	layout := storage.Layout{Root: c.DataDir}
	if err = layout.Init(); err != nil {
		return err
	}
	st, err := repository.Open(filepath.Join(c.DataDir, "db", "report_hosting.sqlite"))
	if err != nil {
		return err
	}
	defer st.Close()
	links := routing.Links{Dir: layout.BySlugDir()}
	if err = service.Recover(context.Background(), st, layout, links); err != nil {
		return fmt.Errorf("startup recovery: %w", err)
	}
	deployer := storage.NewDeployer(layout, st, c.Limits, 100<<20)
	sites := service.NewSites(st, layout, links, deployer, c.ReservedSlugs)
	auth := &service.Auth{Store: st, Params: c.Argon2}
	access := handler.NewAccessVerifier(c.AccessTeamDomain, c.AccessAUD, &handler.RemoteKeys{Team: c.AccessTeamDomain})
	server := &http.Server{Addr: c.ListenAddr, Handler: handler.NewRouter(handler.Deps{Config: c, Sites: sites, Auth: auth, Access: access}), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 3 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}
