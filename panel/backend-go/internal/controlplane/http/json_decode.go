package http

import "encoding/json"

func decodeRawMessageMap(body map[string]json.RawMessage, target any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}
