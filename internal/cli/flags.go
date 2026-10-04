package cli

import "flag"

// installFlagValues binds install's flags for the command handler.
type installFlagValues struct {
	Version        *string
	Channel        *string
	Yes            *bool
	Force          *bool
	Launch         *bool
	KeepWorkspace  *bool
	KeepSources    *bool
	InstallDir     *string
	ToolPreference *string
	RetainBuilds   *bool
}

// installFlags defines install's flags in one place: the command both
// parses through it and renders help from it.
func installFlags() (*flag.FlagSet, *installFlagValues) {
	fs := newFlagSet("install")
	v := &installFlagValues{
		Version:        fs.String("version", "", "pin an exact Aseprite version, or a commit SHA for --channel dev (e.g. 1.3.18.6)"),
		Channel:        fs.String("channel", "", "release channel: stable (default) or dev (requires Git)"),
		Yes:            fs.Bool("yes", false, "skip the confirmation prompt"),
		Force:          fs.Bool("force", false, "replace an Aseprite.app not installed by this tool"),
		Launch:         fs.Bool("launch", false, "launch Aseprite after a successful install"),
		KeepWorkspace:  fs.Bool("keep-workspace", false, "keep the build workspace for repair/rebuild"),
		KeepSources:    fs.Bool("keep-sources", false, "keep older downloaded source trees"),
		InstallDir:     fs.String("install-dir", "", "install under this directory instead of the platform default"),
		ToolPreference: fs.String("tool-preference", "", "prefer \"system\" or \"cached\" CMake/Ninja before downloading"),
		RetainBuilds:   fs.Bool("retain-builds", true, "keep the replaced build for rollback (use --retain-builds=false to discard it)"),
	}
	return fs, v
}

// updateFlagValues binds update's flags.
type updateFlagValues struct {
	Yes    *bool
	Launch *bool
}

func updateFlags() (*flag.FlagSet, *updateFlagValues) {
	fs := newFlagSet("update")
	v := &updateFlagValues{
		Yes:    fs.Bool("yes", false, "skip the confirmation prompt"),
		Launch: fs.Bool("launch", false, "launch Aseprite after a successful update"),
	}
	return fs, v
}

// rebuildFlagValues binds rebuild's flags.
type rebuildFlagValues struct {
	Clean *bool
}

func rebuildFlags() (*flag.FlagSet, *rebuildFlagValues) {
	fs := newFlagSet("rebuild")
	v := &rebuildFlagValues{
		Clean: fs.Bool("clean", false, "start from a fresh source extraction (keeps your user data)"),
	}
	return fs, v
}

// uninstallFlagValues binds uninstall's flags.
type uninstallFlagValues struct {
	Force        *bool
	RemovePrev   *bool
	PurgeSources *bool
	PurgeTools   *bool
	PurgeSkia    *bool
	PurgeLogs    *bool
}

func uninstallFlags() (*flag.FlagSet, *uninstallFlagValues) {
	fs := newFlagSet("uninstall")
	v := &uninstallFlagValues{
		Force:        fs.Bool("force", false, "remove Aseprite.app even if this tool cannot prove it installed it"),
		RemovePrev:   fs.Bool("previous", false, "also remove the retained previous version"),
		PurgeSources: fs.Bool("purge-sources", false, "also remove downloaded Aseprite source trees"),
		PurgeTools:   fs.Bool("purge-tools", false, "also remove downloaded CMake/Ninja caches"),
		PurgeSkia:    fs.Bool("purge-skia", false, "also remove downloaded Skia artifacts"),
		PurgeLogs:    fs.Bool("purge-logs", false, "also remove operation logs"),
	}
	return fs, v
}

// doctorFlagValues binds doctor's flags.
type doctorFlagValues struct {
	AsJSON *bool
}

func doctorFlags() (*flag.FlagSet, *doctorFlagValues) {
	fs := newFlagSet("doctor")
	v := &doctorFlagValues{
		AsJSON: fs.Bool("json", false, "emit machine-readable JSON"),
	}
	return fs, v
}

// plainFlags returns a shared FlagSet for commands that take no flags
// (launch, repair, version) so they still respond to --help.
func plainFlags(name string) *flag.FlagSet {
	return newFlagSet(name)
}
