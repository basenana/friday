package utils

import (
	"encoding/json"
)

func Res2Str(obj interface{}) string {
	raw, err := json.Marshal(obj)
	if err != nil {
		return err.Error()
	}
	return string(raw)
}
