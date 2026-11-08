package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChatStore_LoadSave(t *testing.T) {
	dir := t.TempDir()

	// Initially empty
	msgs, err := loadChatHistory(dir)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}

	// Save some messages
	now := time.Now().Truncate(time.Millisecond)
	testMsgs := []ChatMessage{
		{Role: "user", Content: "hello", Timestamp: now},
		{Role: "assistant", Content: "hi there", Timestamp: now},
		{Role: "user", Content: "how are you?", Timestamp: now.Add(time.Second)},
		{Role: "assistant", Content: "doing well", Timestamp: now.Add(time.Second)},
	}

	if err := saveChatHistory(dir, testMsgs); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Reload and verify
	loaded, err := loadChatHistory(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded) != len(testMsgs) {
		t.Fatalf("expected %d messages, got %d", len(testMsgs), len(loaded))
	}
	for i, msg := range loaded {
		if msg.Role != testMsgs[i].Role {
			t.Errorf("msg[%d] role: got %q, want %q", i, msg.Role, testMsgs[i].Role)
		}
		if msg.Content != testMsgs[i].Content {
			t.Errorf("msg[%d] content: got %q, want %q", i, msg.Content, testMsgs[i].Content)
		}
		if !msg.Timestamp.Equal(testMsgs[i].Timestamp) {
			t.Errorf("msg[%d] timestamp: got %v, want %v", i, msg.Timestamp, testMsgs[i].Timestamp)
		}
	}

	// Verify file exists
	path := filepath.Join(dir, chatHistoryFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestChatStore_EmptyDir(t *testing.T) {
	// Empty config dir should not error
	_, err := loadChatHistory("")
	if err == nil {
		t.Fatal("expected error for empty config dir")
	}
}

func TestChatStore_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, chatHistoryFile)

	// Write corrupt JSON
	if err := os.WriteFile(path, []byte("{invalid json"), 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	// Should fail to parse
	_, err := loadChatHistory(dir)
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}

func TestCapMessages(t *testing.T) {
	msgs := make([]ChatMessage, 10)
	for i := range msgs {
		msgs[i] = ChatMessage{Role: "user", Content: "msg", Timestamp: time.Now()}
	}

	// Cap to 5 — should keep the last 5
	capped := capMessages(msgs, 5)
	if len(capped) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(capped))
	}

	// Cap to 0 — should return all
	all := capMessages(msgs, 0)
	if len(all) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(all))
	}

	// Cap to 20 — should return all
	all2 := capMessages(msgs, 20)
	if len(all2) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(all2))
	}
}

func TestArchiveOldMessages(t *testing.T) {
	dir := t.TempDir()

	old := time.Now().AddDate(0, 0, -60) // 60 days ago
	recent := time.Now().Add(-time.Hour) // 1 hour ago

	msgs := []ChatMessage{
		{Role: "user", Content: "old message", Timestamp: old},
		{Role: "assistant", Content: "old response", Timestamp: old},
		{Role: "user", Content: "recent message", Timestamp: recent},
		{Role: "assistant", Content: "recent response", Timestamp: recent},
	}

	// Archive messages older than 30 days
	active, err := archiveOldMessages(dir, msgs, 30)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Should have 2 active messages (the recent ones)
	if len(active) != 2 {
		t.Fatalf("expected 2 active messages, got %d", len(active))
	}

	// Verify archive file was created
	archivePath := filepath.Join(dir, chatArchiveFile)
	archived, err := loadChatHistoryFromFile(archivePath)
	if err != nil {
		t.Fatalf("load archive: %v", err)
	}
	if len(archived) != 2 {
		t.Fatalf("expected 2 archived messages, got %d", len(archived))
	}
}

func TestArchiveOldMessages_NothingToArchive(t *testing.T) {
	dir := t.TempDir()

	recent := time.Now().Add(-time.Hour)
	msgs := []ChatMessage{
		{Role: "user", Content: "recent", Timestamp: recent},
		{Role: "assistant", Content: "also recent", Timestamp: recent},
	}

	// Archive messages older than 30 days — nothing qualifies
	active, err := archiveOldMessages(dir, msgs, 30)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}

	if len(active) != 2 {
		t.Fatalf("expected 2 active messages, got %d", len(active))
	}

	// Archive file should not exist
	archivePath := filepath.Join(dir, chatArchiveFile)
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatal("archive file should not exist when nothing to archive")
	}
}
