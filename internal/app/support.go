package app

import (
	"context"

	"github.com/akalp/aseprite-builder/internal/aseprite"
	"github.com/akalp/aseprite-builder/internal/logging"
	"github.com/akalp/aseprite-builder/internal/platform"
	"github.com/akalp/aseprite-builder/internal/state"
)

// stateChannelOrDefault normalizes a persisted channel string back to
// its Channel value; only stable exists today, but historical state
// must not break today's behavior.
func stateChannelOrDefault(c string) aseprite.Channel {
	if ch, err := aseprite.ParseChannel(c); err == nil {
		return ch
	}
	return aseprite.ChannelStable
}

// verifyInstalledBundle checks the installed application. The platform
// resolves the executable shape (plain directory on Windows); the
// fallback is the .app bundle layout.
func (s *Service) verifyInstalledBundle(ctx context.Context, current state.InstallState, log *logging.Logger) error {
	if p, ok := s.opts.Platform.(platform.InstalledExecutableProvider); ok {
		exe, err := p.InstalledExecutable()
		if err == nil {
			err = aseprite.VerifyExecutable(ctx, s.opts.Runner, exe, current.AsepriteVersion)
		}
		if err != nil {
			log.Line("repair: installed executable verification failed: %v", err)
			return err
		}
		return nil
	}
	if err := aseprite.VerifyInstalled(ctx, s.opts.Runner, current.AppPath, current.AsepriteVersion); err != nil {
		log.Line("repair: installed bundle verification failed: %v", err)
		return err
	}
	return nil
}
