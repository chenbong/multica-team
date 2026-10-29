package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

func machineValidateSystemPrompt(value string) error {
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return errors.New("system prompt must be valid UTF-8 without NUL bytes")
	}
	return nil
}

// Read and validate before workspace preparation or any HTTP mutation. Keep
// whitespace intact, including trailing newlines from a prompt file.
func machineReadSystemPrompt(cmd *cobra.Command) (*string, error) {
	inline, file := cmd.Flags().Changed("system-prompt"), cmd.Flags().Changed("system-prompt-file")
	if inline && file {
		return nil, errors.New("specify either --system-prompt or --system-prompt-file")
	}
	if !inline && !file {
		return nil, nil
	}
	var value string
	if file {
		path, _ := cmd.Flags().GetString("system-prompt-file")
		if strings.TrimSpace(path) == "" {
			return nil, errors.New("--system-prompt-file requires a file path")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --system-prompt-file: %w", err)
		}
		value = string(data)
	} else {
		value, _ = cmd.Flags().GetString("system-prompt")
	}
	if err := machineValidateSystemPrompt(value); err != nil {
		return nil, err
	}
	return &value, nil
}
