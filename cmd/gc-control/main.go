// Command gc-control runs the standalone GasCity Control Center.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/controlcenter"
)

const supervisorTimeout = 3 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	web, err := embeddedWebFS()
	if err != nil {
		return err
	}
	ping, err := newSupervisorPing(cfg.SupervisorURL, nil)
	if err != nil {
		return err
	}
	app, err := controlcenter.NewApp(cfg, controlcenter.Dependencies{
		StaticFS:       web,
		SupervisorPing: ping,
	})
	if err != nil {
		return err
	}
	log.Printf("GasCity Control Center listening on http://%s", cfg.BindAddress)
	return app.Run(ctx)
}

func parseConfig(args []string) (controlcenter.Config, error) {
	var cfg controlcenter.Config
	flags := flag.NewFlagSet("gc-control", flag.ContinueOnError)
	flags.StringVar(&cfg.BindAddress, "bind", "127.0.0.1:8477", "literal 127.0.0.1 address and numeric port")
	flags.StringVar(&cfg.SupervisorURL, "supervisor-url", "http://127.0.0.1:8372", "GasCity Supervisor base URL")
	flags.StringVar(&cfg.CityName, "city", "", "single GasCity city to control")
	flags.StringVar(&cfg.GCExecutable, "gc", "", "path to the gc executable (resolved from PATH when empty)")
	flags.StringVar(&cfg.PackName, "pack", "", "configured pack command namespace")
	flags.StringVar(&cfg.MayorIdentity, "mayor-identity", "", "qualified configured named-session identity")
	flags.StringVar(&cfg.AssistantTemplate, "assistant-template", "", "configured convoy assistant template")
	flags.StringVar(&cfg.NativeTerminalApp, "terminal-app", "Terminal", "native terminal application")
	if err := flags.Parse(args); err != nil {
		return controlcenter.Config{}, fmt.Errorf("control center: parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return controlcenter.Config{}, fmt.Errorf("control center: unexpected arguments: %v", flags.Args())
	}
	return cfg, nil
}

func newSupervisorPing(baseURL string, client *http.Client) (func(context.Context) error, error) {
	if client == nil {
		client = &http.Client{Timeout: supervisorTimeout}
	}
	typedClient, err := genclient.NewClientWithResponses(baseURL, genclient.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("control center: create Supervisor client: %w", err)
	}
	return func(ctx context.Context) error {
		response, err := typedClient.GetHealthWithResponse(ctx)
		if err != nil {
			return fmt.Errorf("control center: get Supervisor health: %w", err)
		}
		if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
			return fmt.Errorf("control center: get Supervisor health: unexpected status %d", response.StatusCode())
		}
		return nil
	}, nil
}
