package flow

import (
	"friday/config"
	"friday/pkg/friday"
)

var FD *friday.Friday

func init() {
	loader := config.NewConfigLoader()
	cfg, err := loader.GetConfig()
	if err != nil {
		panic(err)
	}

	FD, err = friday.NewFriday(&cfg)
	if err != nil {
		panic(err)
	}
}
