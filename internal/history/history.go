// Package history keeps a local log of deploys in .arch-deploy/history.jsonl,
// next to the config file.
package history

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	Time       time.Time `json:"time"`
	Target     string    `json:"target"`
	Provider   string    `json:"provider"`
	Action     string    `json:"action"`
	Version    string    `json:"version"`
	Commit     string    `json:"commit,omitempty"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Units      []string  `json:"units,omitempty"`
	RolledBack bool      `json:"rolled_back,omitempty"`
	User       string    `json:"user,omitempty"`
}

// File is the history path for a config folder.
func File(dir string) string {
	return filepath.Join(dir, ".arch-deploy", "history.jsonl")
}

// Append adds one entry.
func Append(dir string, e Entry) error {
	path := File(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// Read returns the entries, oldest first; a missing file is an empty history.
func Read(dir string) ([]Entry, error) {
	f, err := os.Open(File(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var e Entry
		if json.Unmarshal(scanner.Bytes(), &e) == nil {
			entries = append(entries, e)
		}
	}
	return entries, scanner.Err()
}
