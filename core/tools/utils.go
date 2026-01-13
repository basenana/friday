package tools

import "encoding/json"

func Res2Str(obj interface{}) string {
	raw, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(raw)
}
