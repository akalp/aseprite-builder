package app

import (
	"fmt"

	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/logging"
	"github.com/akalp/aseprite-builder/internal/state"
)

// Config returns the persisted user configuration, applying defaults
// when the file is absent. The channel is normalized: an empty or
// legacy value resolves to the only supported channel.
func (s *Service) Config() (state.Config, error) {
	cfg, err := s.opts.Store.LoadConfig()
	if err != nil {
		return state.DefaultConfig(), fmt.Errorf("load config: %w", err)
	}
	cfg.Channel = string(validateChannel(cfg.Channel))
	return cfg, nil
}

// SaveConfig validates and persists user configuration.
func (s *Service) SaveConfig(cfg state.Config) error {
	cfg.Channel = string(validateChannel(cfg.Channel))
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
// true can only turn things on.
func applyRunConfig(opts *InstallOptions, cfg state.Config) {
	if cfg.KeepWorkspace {
		opts.KeepWorkspace = true
	}
	if opts.Channel == "" {
		// The persisted channel is the default for new installs;
		// explicit request channels always win.
		opts.Channel = stateChannelOrDefault(cfg.Channel)
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
