package main

import (
	"strings"
)

var appVersion = "dev"

func currentVersion() string {
	if peVersion := strings.TrimSpace(currentExecutablePEVersion()); peVersion != "" {
		return peVersion
	}
	if strings.TrimSpace(appVersion) == "" {
		return "dev"
	}
	return appVersion
}
