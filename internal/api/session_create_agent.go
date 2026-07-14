package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	workdirutil "github.com/gastownhall/gascity/internal/workdir"
)

var errInvalidRequestedSessionWorkDir = errors.New("invalid requested session work directory")

func resolveRequestedSessionWorkDir(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", nil
	}
	if !filepath.IsAbs(requested) {
		return "", fmt.Errorf("%w: path %q must be absolute", errInvalidRequestedSessionWorkDir, requested)
	}
	canonical, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return "", fmt.Errorf("%w: resolving requested session work directory %q: %w", errInvalidRequestedSessionWorkDir, requested, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: statting requested session work directory %q: %w", errInvalidRequestedSessionWorkDir, canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: path %q is not a directory", errInvalidRequestedSessionWorkDir, canonical)
	}
	return filepath.Clean(canonical), nil
}

type agentCreateContext struct {
	Agent        config.Agent
	Alias        string
	ExplicitName string
	Identity     string
	WorkDir      string
}

func (s *Server) resolveAgentCreateContext(template, alias, requestedWorkDir string) (agentCreateContext, error) {
	cfg := s.state.Config()
	if cfg == nil {
		return agentCreateContext{}, fmt.Errorf("no city config loaded")
	}
	agentCfg, ok := resolveSessionTemplateAgent(cfg, template)
	if !ok {
		return agentCreateContext{}, fmt.Errorf("resolved agent template disappeared: %s", template)
	}
	if alias != "" && agentCfg.SupportsMultipleSessions() {
		alias = workdirutil.SessionQualifiedName(s.state.CityPath(), agentCfg, cfg.Rigs, alias, "")
	}
	explicitName, err := sessionExplicitNameForCreate(agentCfg, alias)
	if err != nil {
		return agentCreateContext{}, err
	}
	identity := workdirutil.SessionQualifiedName(s.state.CityPath(), agentCfg, cfg.Rigs, alias, explicitName)
	workDir, err := resolveRequestedSessionWorkDir(requestedWorkDir)
	if err != nil {
		return agentCreateContext{}, err
	}
	if workDir == "" {
		workDir, err = s.resolveSessionWorkDir(agentCfg, identity)
		if err != nil {
			return agentCreateContext{}, err
		}
	}
	return agentCreateContext{
		Agent:        agentCfg,
		Alias:        strings.TrimSpace(alias),
		ExplicitName: explicitName,
		Identity:     identity,
		WorkDir:      workDir,
	}, nil
}
