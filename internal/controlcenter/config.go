// Package controlcenter assembles the standalone GasCity Control Center.
package controlcenter

import (
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

// Config is the complete runtime configuration for one Control Center.
type Config struct {
	BindAddress       string
	SupervisorURL     string
	CityName          string
	GCExecutable      string
	PackName          string
	MayorIdentity     string
	AssistantTemplate string
	NativeTerminalApp string
}

type configDependencies struct {
	lookPath func(string) (string, error)
}

func normalizeConfig(cfg Config, deps configDependencies) (Config, error) {
	cfg.BindAddress = strings.TrimSpace(cfg.BindAddress)
	cfg.SupervisorURL = strings.TrimSpace(cfg.SupervisorURL)
	cfg.CityName = strings.TrimSpace(cfg.CityName)
	cfg.GCExecutable = strings.TrimSpace(cfg.GCExecutable)
	cfg.PackName = strings.TrimSpace(cfg.PackName)
	cfg.MayorIdentity = strings.TrimSpace(cfg.MayorIdentity)
	cfg.AssistantTemplate = strings.TrimSpace(cfg.AssistantTemplate)
	cfg.NativeTerminalApp = strings.TrimSpace(cfg.NativeTerminalApp)

	if err := validateBindAddress(cfg.BindAddress); err != nil {
		return Config{}, err
	}
	normalizedSupervisorURL, err := validateSupervisorURL(cfg.SupervisorURL)
	if err != nil {
		return Config{}, err
	}
	cfg.SupervisorURL = normalizedSupervisorURL

	required := []struct {
		name  string
		value string
	}{
		{name: "city name", value: cfg.CityName},
		{name: "pack name", value: cfg.PackName},
		{name: "Mayor identity", value: cfg.MayorIdentity},
		{name: "assistant template", value: cfg.AssistantTemplate},
	}
	for _, field := range required {
		if field.value == "" {
			return Config{}, fmt.Errorf("control center: %s is required", field.name)
		}
	}

	if cfg.GCExecutable == "" {
		lookPath := deps.lookPath
		if lookPath == nil {
			lookPath = exec.LookPath
		}
		path, err := lookPath("gc")
		if err != nil {
			return Config{}, fmt.Errorf("control center: resolve gc executable: %w", err)
		}
		cfg.GCExecutable = path
	}
	if cfg.NativeTerminalApp == "" {
		cfg.NativeTerminalApp = "Terminal"
	}
	return cfg, nil
}

func validateBindAddress(address string) error {
	if address == "" {
		return fmt.Errorf("control center: bind address is required")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("control center: parse bind address %q: %w", address, err)
	}
	if host != "127.0.0.1" {
		return fmt.Errorf("control center: bind host must be literal 127.0.0.1, got %q", host)
	}
	if port == "" {
		return fmt.Errorf("control center: bind port is required")
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("control center: bind port must be numeric, got %q", port)
		}
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("control center: invalid bind port %q: %w", port, err)
	}
	return nil
}

func validateSupervisorURL(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("control center: supervisor URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("control center: parse supervisor URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("control center: supervisor URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("control center: supervisor URL host is required")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("control center: supervisor URL must not include user information")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", fmt.Errorf("control center: supervisor URL must not include a query")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("control center: supervisor URL must not include a fragment")
	}
	return strings.TrimRight(raw, "/"), nil
}
