package http

import "encoding/json"

// We decode into a raw map first so handlers can detect whether a field was
// present in JSON (including explicit empty values), then decode into the typed
// request struct for normal validation/business logic.
func decodeRawMessageMap(body map[string]json.RawMessage, target any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}
