package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akalp/aseprite-builder/internal/runner"
)

func TestTargetsMatchSpecNaming(t *testing.T) {
	want := []struct {
		os, arch, artifact, binary string
	}{
		{"darwin", "arm64", "aseprite-builder_Darwin_arm64.tar.gz", "aseprite-builder"},
		{"darwin", "amd64", "aseprite-builder_Darwin_x86_64.tar.gz", "aseprite-builder"},
		{"windows", "amd64", "aseprite-builder_Windows_x86_64.zip", "aseprite-builder.exe"},
		{"linux", "amd64", "aseprite-builder_Linux_x86_64.tar.gz", "aseprite-builder"},
	}
	got := Targets()
	if len(got) != len(want) {
		t.Fatalf("Targets() = %d entries, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.OS != w.os || g.Arch != w.arch || g.Artifact != w.artifact || g.Binary != w.binary {
			t.Errorf("Targets()[%d] = %+v, want %+v", i, g, w)
		}
	}
}

func TestVersionPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		env    map[string]string
		result runner.Result
		err    error
		want   string
	}{
		{name: "env wins", env: map[string]string{"VERSION": "2.0.0"}, want: "2.0.0"},
		{name: "env strips v", env: map[string]string{"VERSION": "v2.0.0"}, want: "2.0.0"},
		{name: "git tag", result: runner.Result{Stdout: "v1.2.3\n"}, want: "1.2.3"},
		{name: "git tag without v", result: runner.Result{Stdout: "1.2.3\n"}, want: "1.2.3"},
		{name: "git failure", err: errors.New("git: not found"), want: DevVersion},
		{name: "git exits nonzero", result: runner.Result{ExitCode: 128}, want: DevVersion},
		{name: "git empty output", result: runner.Result{Stdout: "\n"}, want: DevVersion},
		{name: "nothing", want: DevVersion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &runner.FakeRunner{}
			if c.result != (runner.Result{}) {
				f.Results = map[string]runner.Result{"git describe --tags --abbrev=0": c.result}
			}
			if c.err != nil {
				f.Errs = map[string]error{"git describe --tags --abbrev=0": c.err}
			}
			getenv := func(key string) string { return c.env[key] }
			if got := Version(context.Background(), f, getenv); got != c.want {
				t.Errorf("Version() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPackTarGzContainsExactlyTheBuilder(t *testing.T) {
	target := Targets()[0] // darwin/arm64, .tar.gz
	out := filepath.Join(t.TempDir(), target.Artifact)
	payload := []byte("fake builder binary")
	if err := Pack(out, payload, target); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)

	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		if hdr.Name != target.Binary {
			t.Errorf("entry %q, want %q", hdr.Name, target.Binary)
		}
		if hdr.Mode&0o111 == 0 {
			t.Errorf("entry %q is not executable: mode %o", hdr.Name, hdr.Mode)
		}
		if hdr.Size != int64(len(payload)) {
			t.Errorf("entry size = %d, want %d", hdr.Size, len(payload))
		}
	}
	if len(names) != 1 {
		t.Fatalf("entries = %v, want exactly one", names)
	}
}

func TestPackZipContainsExactlyTheBuilder(t *testing.T) {
	target := Targets()[2] // windows/amd64, .zip
	out := filepath.Join(t.TempDir(), target.Artifact)
	payload := []byte("fake builder exe")
	if err := Pack(out, payload, target); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) != 1 {
		t.Fatalf("entries = %d, want exactly one", len(zr.File))
	}
	entry := zr.File[0]
	if entry.Name != target.Binary {
		t.Errorf("entry %q, want %q", entry.Name, target.Binary)
	}
	if !strings.HasSuffix(strings.ToLower(entry.Name), ".exe") {
		t.Errorf("Windows payload %q must end in .exe", entry.Name)
	}
	rc, err := entry.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(payload) {
		t.Errorf("payload = %q, want %q", data, payload)
	}
}

func TestPackIsReproducible(t *testing.T) {
	target := Targets()[3]
	dir := t.TempDir()
	first := filepath.Join(dir, "first-"+target.Artifact)
	second := filepath.Join(dir, "second-"+target.Artifact)
	payload := []byte("same bytes")
	for _, out := range []string{first, second} {
		if err := Pack(out, payload, target); err != nil {
			t.Fatalf("Pack(%s): %v", out, err)
		}
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("packing the same binary twice must be deterministic")
	}
}
