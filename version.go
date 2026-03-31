package main

import (
	"strings"
)

var appVersion = "dev"

func currentVersion() string {
	if strings.TrimSpace(appVersion) == "" {
		return "dev"
	}
	return appVersion
}
