// Command gc-control runs the standalone GasCity Control Center.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/controlcenter"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

const supervisorTimeout = 3 * time.Second

type runDependencies struct {
	webFS         func() (fs.FS, error)
	newSupervisor func(string, string, *http.Client) (controlcenter.SupervisorBundle, error)
}

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
	return runWithDependencies(ctx, args, runDependencies{
		webFS:         embeddedWebFS,
		newSupervisor: newSupervisorBundle,
	})
}

func runWithDependencies(ctx context.Context, args []string, deps runDependencies) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	if deps.webFS == nil || deps.newSupervisor == nil {
		return fmt.Errorf("control center: runtime dependencies are required")
	}
	web, err := deps.webFS()
	if err != nil {
		return err
	}
	app, err := controlcenter.NewApp(cfg, controlcenter.Dependencies{
		StaticFS: web,
		SupervisorFactory: func(baseURL, cityName string) (controlcenter.SupervisorBundle, error) {
			return deps.newSupervisor(baseURL, cityName, nil)
		},
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

func newSupervisorClient(baseURL string, client *http.Client) (*genclient.ClientWithResponses, error) {
	if client == nil {
		client = &http.Client{}
	}
	typedClient, err := genclient.NewClientWithResponses(baseURL, genclient.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("control center: create Supervisor client: %w", err)
	}
	return typedClient, nil
}

func newSupervisorBundle(baseURL, cityName string, client *http.Client) (controlcenter.SupervisorBundle, error) {
	typedClient, err := newSupervisorClient(baseURL, client)
	if err != nil {
		return controlcenter.SupervisorBundle{}, err
	}
	stateClient, err := gcstate.NewClient(cityName, typedClient, typedClient.ClientInterface)
	if err != nil {
		return controlcenter.SupervisorBundle{}, fmt.Errorf("control center: create Supervisor state client: %w", err)
	}
	state, err := gcstate.NewService(stateClient)
	if err != nil {
		return controlcenter.SupervisorBundle{}, fmt.Errorf("control center: create Supervisor state service: %w", err)
	}
	events, err := gcstate.NewHub(stateClient)
	if err != nil {
		return controlcenter.SupervisorBundle{}, fmt.Errorf("control center: create Supervisor event hub: %w", err)
	}
	ping := func(ctx context.Context) error {
		callCtx, cancel := context.WithTimeout(ctx, supervisorTimeout)
		defer cancel()
		response, err := typedClient.GetHealthWithResponse(callCtx)
		if err != nil {
			return fmt.Errorf("control center: get Supervisor health: %w", err)
		}
		if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
			return fmt.Errorf("control center: get Supervisor health: unexpected status %d", response.StatusCode())
		}
		return nil
	}
	return controlcenter.SupervisorBundle{Ping: ping, State: state, Events: events}, nil
}
