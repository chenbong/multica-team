package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// A project-wide preference deliberately carries no machine identity or mode.
// Retain native local_directory resources for existing per-machine projects.
func validateProjectWorkingDirectoryRef(raw json.RawMessage) (json.RawMessage, error) {
	var ref struct {
		LocalPath string `json:"local_path"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&ref); err != nil {
		return nil, errors.New("working_directory: only local_path is supported")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, errors.New("working_directory: invalid configuration")
	}
	ref.LocalPath = strings.TrimSpace(ref.LocalPath)
	if !isAbsoluteLocalPath(ref.LocalPath) || strings.ContainsRune(ref.LocalPath, 0) {
		return nil, errors.New("working_directory: local_path must be an absolute path")
	}
	return json.Marshal(ref)
}
