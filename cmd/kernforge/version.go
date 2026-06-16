package main

import (
	"runtime/debug"
	"strings"
)

var appVersion = "dev"
var appCommit = ""
var appBuildTime = ""

type KernforgeBuildIdentity struct {
	Version     string `json:"version,omitempty"`
	Commit      string `json:"commit,omitempty"`
	BuildTime   string `json:"build_time,omitempty"`
	StampSource string `json:"stamp_source,omitempty"`
}

func currentVersion() string {
	if peVersion := strings.TrimSpace(currentExecutablePEVersion()); peVersion != "" {
		return peVersion
	}
	if strings.TrimSpace(appVersion) == "" {
		return "dev"
	}
	return appVersion
}

func currentBuildIdentity() KernforgeBuildIdentity {
	identity := KernforgeBuildIdentity{
		Version: currentVersion(),
	}
	if commit := strings.TrimSpace(appCommit); commit != "" {
		identity.Commit = commit
		identity.StampSource = "ldflags"
	}
	if buildTime := strings.TrimSpace(appBuildTime); buildTime != "" {
		identity.BuildTime = buildTime
		if identity.StampSource == "" {
			identity.StampSource = "ldflags"
		}
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if identity.Commit == "" {
					identity.Commit = strings.TrimSpace(setting.Value)
					if identity.Commit != "" && identity.StampSource == "" {
						identity.StampSource = "go_build_info"
					}
				}
			case "vcs.time":
				if identity.BuildTime == "" {
					identity.BuildTime = strings.TrimSpace(setting.Value)
					if identity.BuildTime != "" && identity.StampSource == "" {
						identity.StampSource = "go_build_info"
					}
				}
			case "vcs.modified":
				if strings.EqualFold(strings.TrimSpace(setting.Value), "true") && identity.StampSource == "go_build_info" {
					identity.StampSource = "go_build_info_modified"
				}
			}
		}
	}
	if identity.StampSource == "" {
		identity.StampSource = "unstamped"
	}
	return identity
}

// currentBuildStamp returns a short, banner-friendly build identifier (the VCS
// commit, suffixed +modified when the working tree was dirty at build time, or
// "unstamped" when no VCS info is embedded). Surfacing it makes a stale
// transferred binary obvious at a glance instead of being mistaken for the
// latest source.
func currentBuildStamp() string {
	identity := currentBuildIdentity()
	commit := strings.TrimSpace(identity.Commit)
	if commit == "" {
		return "unstamped"
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if identity.StampSource == "go_build_info_modified" {
		commit += "+modified"
	}
	return commit
}

func kernforgeBuildIdentitySummary(identity KernforgeBuildIdentity) string {
	parts := []string{}
	if version := strings.TrimSpace(identity.Version); version != "" {
		parts = append(parts, "version="+version)
	}
	if commit := strings.TrimSpace(identity.Commit); commit != "" {
		parts = append(parts, "commit="+commit)
	}
	if buildTime := strings.TrimSpace(identity.BuildTime); buildTime != "" {
		parts = append(parts, "build_time="+buildTime)
	}
	if source := strings.TrimSpace(identity.StampSource); source != "" {
		parts = append(parts, "source="+source)
	}
	return strings.Join(parts, " ")
}
