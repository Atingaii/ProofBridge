package bridge

import "encoding/json"

func appServerResolvedModel(res json.RawMessage) string {
	data := appServerResultMap(res)
	return firstNonEmpty(firstString(data, "model"), nestedString(data, "thread", "model"))
}

func usageWithResolvedModel(raw json.RawMessage, model string) json.RawMessage {
	if model == "" || len(raw) == 0 {
		return raw
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return raw
	}
	if firstString(data, "model") != "" {
		return raw
	}
	data["model"] = model
	encoded, err := json.Marshal(data)
	if err != nil {
		return raw
	}
	return encoded
}
