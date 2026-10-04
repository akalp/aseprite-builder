package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/logging"
	"github.com/akalp/aseprite-builder/internal/state"
)

// Config returns the persisted user configuration, applying defaults
// when the file is absent. The channel and tool preference are
// normalized: empty or legacy values resolve to the defaults.
func (s *Service) Config() (state.Config, error) {
	cfg, err := s.opts.Store.LoadConfig()
	if err != nil {
		return state.DefaultConfig(), fmt.Errorf("load config: %w", err)
	}
	cfg.Channel = string(validateChannel(cfg.Channel))
	cfg.ToolPreference = normalizeToolPreference(cfg.ToolPreference)
	return cfg, nil
}

// SaveConfig validates and persists user configuration.
func (s *Service) SaveConfig(cfg state.Config) error {
	cfg.Channel = string(validateChannel(cfg.Channel))
	cfg.ToolPreference = normalizeToolPreference(cfg.ToolPreference)
	cfg.InstallDir = strings.TrimSpace(cfg.InstallDir)
	if cfg.InstallDir != "" && !filepath.IsAbs(cfg.InstallDir) {
		return fmt.Errorf("install directory must be an absolute path: %q", cfg.InstallDir)
	}
	if err := s.opts.Store.SaveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func validateChannel(c string) aseprite.Channel {
	ch, err := aseprite.ParseChannel(c)
	if err != nil {
		// Unknown channel values degrade to the default instead of
		// failing the whole config load.
		return aseprite.ChannelStable
	}
	return ch
}

// normalizeToolPreference canonicalizes the advanced tool preference:
// "system" (the default) prefers a compatible system tool; "cached"
// prefers the builder's download cache. Unknown values degrade to the
// default.
func normalizeToolPreference(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "cached") {
		return "cached"
	}
	return "system"
}

// runConfig loads the persisted configuration for one pipeline
// invocation. A broken config.json never blocks the primary task;
// defaults are used instead.
func (s *Service) runConfig() state.Config {
	cfg, err := s.opts.Store.LoadConfig()
	if err != nil {
		return state.DefaultConfig()
	}
	return cfg
}

// applyRunConfig merges persisted user preferences into per-run
// options. Explicit request options are never downgraded: a persisted
// true can only turn things on (boolean preferences that default true
// are applied only when the run leaves them unset).
func applyRunConfig(opts *InstallOptions, cfg state.Config) {
	if cfg.KeepWorkspace {
		opts.KeepWorkspace = true
	}
	if cfg.KeepSources {
		opts.KeepSources = true
	}
	if opts.Channel == "" {
		// The persisted channel is the default for new installs;
		// explicit request channels always win.
		opts.Channel = stateChannelOrDefault(cfg.Channel)
	}
	if opts.InstallDir == "" {
		opts.InstallDir = cfg.InstallDir
	}
	if opts.ToolPreference == "" {
		opts.ToolPreference = normalizeToolPreference(cfg.ToolPreference)
	}
	if opts.RetainBuilds == nil {
		retain := cfg.RetainBuilds
		opts.RetainBuilds = &retain
	}
}

// verboseSink wraps a ProgressSink so each event is also recorded in
// the operation log when the user enabled verbose logging —
// independent of whether the frontend passes a sink at all.
func verboseSink(sink ProgressSink, cfg state.Config, log *logging.Logger) ProgressSink {
	if !cfg.Verbose || log == nil {
		return sink
	}
	return func(e ProgressEvent) {
		if sink != nil {
			sink(e)
		}
		log.Line("progress: stage=%q message=%q current=%d total=%d", e.Stage.String(), e.Message, e.Current, e.Total)
	}
}
