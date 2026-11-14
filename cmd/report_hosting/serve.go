package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"report_hosting/internal/config"
	"report_hosting/internal/handler"
	"report_hosting/internal/repository"
	"report_hosting/internal/routing"
	"report_hosting/internal/service"
	"report_hosting/internal/storage"
)

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
	// Driven adapters: on-disk layout and SQLite.
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
	// Repair disk and database before the process accepts traffic.
	if err = service.Recover(context.Background(), st, layout, links); err != nil {
		return fmt.Errorf("startup recovery: %w", err)
	}
	deployer := storage.NewDeployer(layout, st, c.Limits, 100<<20)
	sites := service.NewSites(st, layout, links, deployer, c.ReservedSlugs)
	auth := &service.Auth{Store: st, Params: c.Argon2}
	// Driving adapter: HTTP is the only entry point of this process.
	access := handler.NewAccessVerifier(c.AccessTeamDomain, c.AccessAUD, &handler.RemoteKeys{Team: c.AccessTeamDomain})
	server := &http.Server{
		Addr: c.ListenAddr,
		Handler: handler.NewRouter(handler.Deps{
			Config: c, Sites: sites, Auth: auth, Access: access,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
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
