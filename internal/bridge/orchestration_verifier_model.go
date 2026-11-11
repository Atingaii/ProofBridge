package bridge

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// exec --json can report tokens without a model. Read bounded metadata from
// that invocation's native session, using the same home as the CLI command.
func resolveCodexVerifierModel(output *silentVerifierOutput, codexHome string) {
	if len(output.Usage) == 0 || appServerResolvedModel(output.Usage) != "" || !validNativeSessionID(output.ThreadID) {
		return
	}
	if codexHome == "" {
		codexHome = os.Getenv("CODEX_HOME")
	}
	if codexHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		codexHome = filepath.Join(home, ".codex")
	}
	path, err := findNativeSessionLog(filepath.Join(codexHome, "sessions"), output.ThreadID)
	if err != nil {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	// Verify the native ID before reading a bounded tail. A resumed session may
	// have changed models: the first turn's model is not proof of today's call.
	head := bufio.NewReader(io.LimitReader(file, 64<<10))
	line, err := readJSONLLine(head, 64<<10)
	var meta struct {
		Type    string
		Payload struct{ ID string }
	}
	if err != nil || json.Unmarshal(line, &meta) != nil || meta.Type != "session_meta" || meta.Payload.ID != output.ThreadID {
		return
	}
	info, err := file.Stat()
	if err != nil {
		return
	}
	const limit = 64 << 20
	offset := max(int64(0), info.Size()-limit)
	if _, err = file.Seek(offset, io.SeekStart); err != nil {
		return
	}
	reader := bufio.NewReader(io.LimitReader(file, limit))
	if offset > 0 {
		if _, err := reader.ReadBytes('\n'); err != nil {
			return
		}
	}
	model := ""
	for {
		line, err := readJSONLLine(reader, limit)
		if err != nil {
			if errors.Is(err, io.EOF) && model != "" {
				output.Usage = usageWithResolvedModel(output.Usage, model)
			}
			return
		}
		var record struct {
			Type    string
			Payload struct {
				Type, Model    string
				ThreadSettings struct{ Model string } `json:"thread_settings"`
			}
		}
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		switch record.Type {
		case "session_meta", "turn_context":
			if record.Payload.Model != "" {
				model = record.Payload.Model
			}
		case "event_msg":
			if record.Payload.Type == "thread_settings_applied" && record.Payload.ThreadSettings.Model != "" {
				model = record.Payload.ThreadSettings.Model
			}
		}
	}
}
