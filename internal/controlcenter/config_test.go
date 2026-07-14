package controlcenter

import (
	"errors"
	"fmt"
	"testing"
)

func validTestConfig() Config {
	return Config{
		BindAddress:       "127.0.0.1:0",
		SupervisorURL:     "http://127.0.0.1:8372/",
		CityName:          "taxdome",
		GCExecutable:      "/opt/gascity/bin/gc",
		PackName:          "gascity",
		MayorIdentity:     "taxdome/lead.operator",
		AssistantTemplate: "taxdome/workers.convoy_assistant",
	}
}

func TestConfigAcceptsLiteralLoopbackAndNormalizesValues(t *testing.T) {
	cfg := validTestConfig()
	cfg.CityName = " taxdome "
	cfg.PackName = " gascity "
	cfg.MayorIdentity = " taxdome/lead.operator "
	cfg.AssistantTemplate = " taxdome/workers.convoy_assistant "

	got, err := normalizeConfig(cfg, configDependencies{
		lookPath: func(string) (string, error) {
			t.Fatal("lookPath called for an explicit executable")
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("normalizeConfig: %v", err)
	}

	if got.BindAddress != "127.0.0.1:0" {
		t.Errorf("BindAddress = %q, want literal loopback", got.BindAddress)
	}
	if got.SupervisorURL != "http://127.0.0.1:8372" {
		t.Errorf("SupervisorURL = %q, want trailing slash removed", got.SupervisorURL)
	}
	if got.NativeTerminalApp != "Terminal" {
		t.Errorf("NativeTerminalApp = %q, want Terminal", got.NativeTerminalApp)
	}
	if got.CityName != "taxdome" || got.PackName != "gascity" {
		t.Errorf("names were not trimmed: city=%q pack=%q", got.CityName, got.PackName)
	}
	if got.MayorIdentity != "taxdome/lead.operator" {
		t.Errorf("MayorIdentity = %q, want configured identity unchanged apart from whitespace", got.MayorIdentity)
	}
	if got.AssistantTemplate != "taxdome/workers.convoy_assistant" {
		t.Errorf("AssistantTemplate = %q", got.AssistantTemplate)
	}
}

func TestConfigRejectsEveryNonLiteralLoopbackBind(t *testing.T) {
	for _, address := range []string{
		"localhost:8080",
		"[::1]:8080",
		"0.0.0.0:8080",
		"192.168.1.10:8080",
		"example.test:8080",
		"127.0.0.2:8080",
		":8080",
		"127.0.0.1",
		"127.0.0.1:http",
		"127.0.0.1:65536",
		"127.0.0.1:-1",
	} {
		t.Run(address, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.BindAddress = address
			if _, err := normalizeConfig(cfg, configDependencies{}); err == nil {
				t.Fatalf("normalizeConfig accepted %q", address)
			}
		})
	}
}

func TestConfigRequiresConfiguredIdentityAndNames(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Config)
	}{
		{"bind address", func(cfg *Config) { cfg.BindAddress = " " }},
		{"supervisor URL", func(cfg *Config) { cfg.SupervisorURL = " " }},
		{"city name", func(cfg *Config) { cfg.CityName = " " }},
		{"pack name", func(cfg *Config) { cfg.PackName = " " }},
		{"Mayor identity", func(cfg *Config) { cfg.MayorIdentity = " " }},
		{"assistant template", func(cfg *Config) { cfg.AssistantTemplate = " " }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validTestConfig()
			tt.set(&cfg)
			if _, err := normalizeConfig(cfg, configDependencies{}); err == nil {
				t.Fatalf("normalizeConfig accepted blank %s", tt.name)
			}
		})
	}
}

func TestConfigReportsRequiredFieldsInContractOrder(t *testing.T) {
	cfg := validTestConfig()
	cfg.CityName = ""
	cfg.PackName = ""
	cfg.MayorIdentity = ""
	cfg.AssistantTemplate = ""

	for attempt := 0; attempt < 100; attempt++ {
		_, err := normalizeConfig(cfg, configDependencies{})
		if err == nil || err.Error() != "control center: city name is required" {
			t.Fatalf("attempt %d: error = %v, want city-name error first", attempt, err)
		}
	}
}

func TestConfigValidatesSupervisorURL(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1:8372",
		"ftp://127.0.0.1:8372",
		"http:///missing-host",
		"http://user:secret@127.0.0.1:8372",
		"http://127.0.0.1:8372?debug=true",
		"http://127.0.0.1:8372#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			cfg := validTestConfig()
			cfg.SupervisorURL = raw
			if _, err := normalizeConfig(cfg, configDependencies{}); err == nil {
				t.Fatalf("normalizeConfig accepted SupervisorURL %q", raw)
			}
		})
	}
}

func TestConfigResolvesBlankGCExecutableWithInjectedLookPath(t *testing.T) {
	cfg := validTestConfig()
	cfg.GCExecutable = " "
	var lookedUp string

	got, err := normalizeConfig(cfg, configDependencies{
		lookPath: func(name string) (string, error) {
			lookedUp = name
			return "/custom/bin/gc", nil
		},
	})
	if err != nil {
		t.Fatalf("normalizeConfig: %v", err)
	}
	if lookedUp != "gc" {
		t.Fatalf("lookPath called with %q, want gc", lookedUp)
	}
	if got.GCExecutable != "/custom/bin/gc" {
		t.Errorf("GCExecutable = %q", got.GCExecutable)
	}
}

func TestConfigReportsGCExecutableLookupFailure(t *testing.T) {
	cfg := validTestConfig()
	cfg.GCExecutable = ""
	want := errors.New("not installed")

	_, err := normalizeConfig(cfg, configDependencies{
		lookPath: func(string) (string, error) { return "", want },
	})
	if !errors.Is(err, want) {
		t.Fatalf("normalizeConfig error = %v, want wrapped %v", err, want)
	}
	if got := fmt.Sprint(err); got == want.Error() {
		t.Fatalf("lookup error lacks operation context: %q", got)
	}
}
