package codec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON parses JSON data into a tree of native Go types matching the
// value types used by the codec: nil, bool, int, float64, string, []any, and
// map[string]any.
func UnmarshalJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return convertJSONValue(raw)
}

func convertJSONValue(v any) (any, error) {
	switch val := v.(type) {
	case map[string]any:
		for k, v := range val {
			cv, err := convertJSONValue(v)
			if err != nil {
				return nil, err
			}
			val[k] = cv
		}
		return val, nil
	case []any:
		for i, v := range val {
			cv, err := convertJSONValue(v)
			if err != nil {
				return nil, err
			}
			val[i] = cv
		}
		return val, nil
	case json.Number:
		if n, err := val.Int64(); err == nil {
			return int(n), nil
		}
		f, err := val.Float64()
		if err != nil {
			return nil, fmt.Errorf("invalid number %v", val)
		}
		return f, nil
	default:
		return val, nil
	}
}
