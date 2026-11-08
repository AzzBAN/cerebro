package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// chatHistoryFile is the name of the JSON file storing copilot chat history.
const chatHistoryFile = "copilot_history.json"

// chatArchiveFile is the name of the archive file for old messages.
const chatArchiveFile = "copilot_history_archive.json"

// chatMaxMessages is the maximum number of messages to retain in the active history.
const chatMaxMessages = 500

// chatArchiveAgeDays is the number of days after which messages are archived.
const chatArchiveAgeDays = 30

// chatHistorySchema is the JSON schema for the chat history file.
type chatHistorySchema struct {
	Messages []ChatMessage `json:"messages"`
}

// loadChatHistory reads the chat history from disk. Returns an empty slice if
// the file doesn't exist or is unreadable (fail-open).
func loadChatHistory(configDir string) ([]ChatMessage, error) {
	if configDir == "" {
		return nil, fmt.Errorf("configDir is empty")
	}

	path := filepath.Join(configDir, chatHistoryFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []ChatMessage{}, nil
		}
		return nil, fmt.Errorf("read chat history: %w", err)
	}

	var schema chatHistorySchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parse chat history: %w", err)
	}

	return schema.Messages, nil
}

// saveChatHistory writes the chat history to disk atomically. Writes to a
// temporary file first, then renames to avoid partial writes.
func saveChatHistory(configDir string, messages []ChatMessage) error {
	if configDir == "" {
		return fmt.Errorf("configDir is empty")
	}

	schema := chatHistorySchema{Messages: messages}
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal chat history: %w", err)
	}

	path := filepath.Join(configDir, chatHistoryFile)
	tmpPath := path + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write chat history tmp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename chat history: %w", err)
	}

	return nil
}

// archiveOldMessages moves messages older than maxAgeDays to the archive file.
// Returns the trimmed list of active messages. The archive is appended to, not
// overwritten.
func archiveOldMessages(configDir string, messages []ChatMessage, maxAgeDays int) ([]ChatMessage, error) {
	if maxAgeDays <= 0 || len(messages) == 0 {
		return messages, nil
	}

	cutoff := time.Now().AddDate(0, 0, -maxAgeDays)
	var active []ChatMessage
	var archived []ChatMessage

	for _, msg := range messages {
		if msg.Timestamp.Before(cutoff) {
			archived = append(archived, msg)
		} else {
			active = append(active, msg)
		}
	}

	if len(archived) == 0 {
		return active, nil
	}

	// Load existing archive (if any) and append new messages.
	archivePath := filepath.Join(configDir, chatArchiveFile)
	existingArchive, _ := loadChatHistoryFromFile(archivePath)
	existingArchive = append(existingArchive, archived...)

	if err := saveChatHistoryToFile(archivePath, existingArchive); err != nil {
		// Archive failed, but we still return the trimmed active list.
		// Log this error upstream if needed.
		return active, fmt.Errorf("archive old messages: %w", err)
	}

	return active, nil
}

// loadChatHistoryFromFile is a helper that reads from an arbitrary path.
func loadChatHistoryFromFile(path string) ([]ChatMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []ChatMessage{}, nil
		}
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}

	var schema chatHistorySchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parse file %s: %w", path, err)
	}

	return schema.Messages, nil
}

// saveChatHistoryToFile is a helper that writes to an arbitrary path.
func saveChatHistoryToFile(path string, messages []ChatMessage) error {
	schema := chatHistorySchema{Messages: messages}
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal to %s: %w", path, err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write tmp %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s: %w", path, err)
	}

	return nil
}

// capMessages trims the message list to the most recent maxMessages.
func capMessages(messages []ChatMessage, maxMessages int) []ChatMessage {
	if maxMessages <= 0 || len(messages) <= maxMessages {
		return messages
	}
	return messages[len(messages)-maxMessages:]
}
