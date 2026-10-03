package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	cfg, err := s.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Channel != "stable" {
		t.Errorf("Channel = %q, want stable", cfg.Channel)
	}
	if cfg.KeepWorkspace {
		t.Error("KeepWorkspace should default false")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	cfg := Config{Channel: "stable", KeepWorkspace: true, Verbose: true}
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := s.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got != cfg {
		t.Errorf("config = %+v, want %+v", got, cfg)
	}
}

func TestInstallRoundTrip(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	st := InstallState{
		AsepriteVersion: "1.3.18.6",
		TagName:         "v1.3.18.6",
		Channel:         "stable",
		SourceURL:       "https://example.com/src.zip",
		SourceSHA256:    "abc",
		SkiaTag:         "m124-08a5439a6b",
		BuildContract:   "build.sh-auto-v1",
		AppPath:         "/Users/x/Applications/Aseprite.app",
		InstalledAt:     time.Now().Truncate(time.Second).UTC(),
		WorkspacePath:   "/ws/aseprite-1.3.18.6",
	}
	if err := s.SaveInstall(st); err != nil {
		t.Fatalf("SaveInstall: %v", err)
	}
	got, err := s.LoadInstall()
	if err != nil {
		t.Fatalf("LoadInstall: %v", err)
	}
	if got.AsepriteVersion != st.AsepriteVersion || got.AppPath != st.AppPath || got.SkiaTag != st.SkiaTag {
		t.Errorf("install = %+v, want %+v", got, st)
	}
}

func TestLoadInstallNoState(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	_, err := s.LoadInstall()
	if !errors.Is(err, ErrNoState) {
		t.Errorf("err = %v, want ErrNoState", err)
	}
}

func TestSaveInstallRefusesIncomplete(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.SaveInstall(InstallState{AsepriteVersion: "1.0"}); err == nil {
		t.Error("missing AppPath should refuse")
	}
	if err := s.SaveInstall(InstallState{AppPath: "/x/Aseprite.app"}); err == nil {
		t.Error("missing version should refuse")
	}
}

func TestClearInstall(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	st := InstallState{AsepriteVersion: "1.0", AppPath: "/x/Aseprite.app"}
	if err := s.SaveInstall(st); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearInstall(); err != nil {
		t.Fatalf("ClearInstall: %v", err)
	}
	if err := s.ClearInstall(); err != nil {
		t.Errorf("second ClearInstall (idempotent): %v", err)
	}
	if _, err := s.LoadInstall(); !errors.Is(err, ErrNoState) {
		t.Errorf("LoadInstall after clear = %v, want ErrNoState", err)
	}
}

func TestWriteJSONAtomicTempCleanup(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	if err := s.SaveConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file %s leaked", e.Name())
		}
	}
}

func TestLoadInstallCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	if err := os.WriteFile(s.InstallPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadInstall(); err == nil {
		t.Error("corrupt JSON should error")
	}
}
