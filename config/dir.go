package config

import (
	"os"
	"path"
)

const (
	DefaultConfigBase   = "friday.conf"
	defaultWorkDir      = ".nana"
	defaultSysLocalPath = "/var/lib/nanafs"
)

func LocalUserPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return defaultSysLocalPath
	}
	return path.Join(homeDir, defaultWorkDir)
}
