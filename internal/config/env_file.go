package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type envLookup func(string) (string, bool)

// configurationEnvironment overlays only an explicitly selected file. It never
// mutates the process environment or evaluates shell expressions.
func configurationEnvironment() (envLookup, error) {
	path := os.Getenv("CODEX_BRIDGE_ENV_FILE")
	if path == "" {
		return os.LookupEnv, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read CODEX_BRIDGE_ENV_FILE; check its path and file permissions")
	}
	if len(data) > 1<<20 {
		return nil, errors.New("CODEX_BRIDGE_ENV_FILE exceeds 1 MiB")
	}
	values, err := parseEnvironmentFile(data)
	if err != nil {
		return nil, err
	}
	return func(key string) (string, bool) {
		if value, exists := os.LookupEnv(key); exists {
			return value, true
		}
		value, exists := values[key]
		return value, exists
	}, nil
}

func parseEnvironmentFile(data []byte) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, raw, exists := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !exists || !validEnvironmentKey(key) {
			return nil, fmt.Errorf("invalid CODEX_BRIDGE_ENV_FILE entry at line %d; expected NAME=value", lineNumber)
		}
		value, err := environmentValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("invalid CODEX_BRIDGE_ENV_FILE value at line %d; check quoting", lineNumber)
		}
		values[key] = value
	}
	if scanner.Err() != nil {
		return nil, errors.New("invalid CODEX_BRIDGE_ENV_FILE; an entry exceeds 64 KiB")
	}
	return values, nil
}

func validEnvironmentKey(key string) bool {
	if key == "" {
		return false
	}
	for i, c := range key {
		if c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

func environmentValue(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", errors.New("null byte")
	}
	if raw == "" {
		return "", nil
	}
	if raw[0] != '\'' && raw[0] != '"' {
		for i := 1; i < len(raw); i++ {
			if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
				raw = raw[:i]
				break
			}
		}
		return strings.TrimSpace(raw), nil
	}
	quote := raw[0]
	end := -1
	escaped := false
	for i := 1; i < len(raw); i++ {
		if quote == '"' && escaped {
			escaped = false
			continue
		}
		if quote == '"' && raw[i] == '\\' {
			escaped = true
			continue
		}
		if raw[i] == quote {
			end = i
			break
		}
	}
	if end < 0 {
		return "", errors.New("unclosed quote")
	}
	tail := strings.TrimSpace(raw[end+1:])
	if tail != "" && !strings.HasPrefix(tail, "#") {
		return "", errors.New("unexpected trailing text")
	}
	if quote == '\'' {
		return raw[1:end], nil
	}
	return strconv.Unquote(raw[:end+1])
}
