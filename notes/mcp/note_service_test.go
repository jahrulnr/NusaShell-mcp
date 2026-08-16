package main

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T) (*NoteService, string) {
	t.Helper()
	dir := t.TempDir()
	dataFile := filepath.Join(dir, "notes.json")
	t.Setenv("NUSASHELL_NOTES_DATA_FILE", dataFile)
	t.Setenv("NUSASHELL_USER_DATA", "")
	t.Setenv("NUSASHELL_DATA_DIR", "")
	return NewNoteService(dataFile), dataFile
}

func TestNoteServiceLoad_Empty(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(svc.notes) != 0 {
		t.Errorf("expected 0 notes, got %d", len(svc.notes))
	}
	if svc.nextID != 1 {
		t.Errorf("expected nextID=1, got %d", svc.nextID)
	}
}

func TestNoteServiceLoad_Existing(t *testing.T) {
	svc, dataFile := newTestService(t)
	writeJSON(t, dataFile, `{"notes":[{"id":1,"text":"first","createdAt":"2026-01-01T00:00:00Z"},{"id":3,"text":"third","createdAt":"2026-01-03T00:00:00Z"}]}`)

	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(svc.notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(svc.notes))
	}
	if svc.nextID != 4 {
		t.Errorf("expected nextID=4, got %d", svc.nextID)
	}
}

func TestNoteServiceLoad_MigrateOldFormat(t *testing.T) {
	svc, dataFile := newTestService(t)
	writeJSON(t, dataFile, `{"notes":[{"id":1,"text":"old note","createdAt":"2026-01-01T00:00:00Z"}]}`)

	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(svc.notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(svc.notes))
	}
	if len(svc.notes[0].Tags) != 0 {
		t.Errorf("expected empty tags, got %v", svc.notes[0].Tags)
	}
	if svc.notes[0].UpdatedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("expected updatedAt=createdAt, got %s", svc.notes[0].UpdatedAt)
	}
}

func TestNoteServiceCreate_WithTags(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()

	n, err := svc.Create("hello world", []string{"work", "idea"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if n.ID != 1 {
		t.Errorf("expected id=1, got %d", n.ID)
	}
	if n.Text != "hello world" {
		t.Errorf("expected text='hello world', got %q", n.Text)
	}
	if len(n.Tags) != 2 || n.Tags[0] != "work" || n.Tags[1] != "idea" {
		t.Errorf("expected tags=[work,idea], got %v", n.Tags)
	}
	if n.CreatedAt == "" || n.CreatedAt != n.UpdatedAt {
		t.Errorf("expected createdAt==updatedAt, got %q / %q", n.CreatedAt, n.UpdatedAt)
	}
}

func TestNoteServiceCreate_NoTags(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()

	n, err := svc.Create("no tags", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(n.Tags) != 0 {
		t.Errorf("expected empty tags, got %v", n.Tags)
	}
}

func TestNoteServiceCreate_EmptyText(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()

	if _, err := svc.Create("", nil); err == nil {
		t.Error("expected error for empty text")
	}
}

func TestNoteServiceCreate_TooManyTags(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()

	tags := make([]string, 21)
	for i := range tags {
		tags[i] = "tag"
	}
	if _, err := svc.Create("text", tags); err == nil {
		t.Error("expected error for too many tags")
	}
}

func TestNoteServiceList_All(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	_, _ = svc.Create("note 1", []string{"work"})
	_, _ = svc.Create("note 2", []string{"personal"})

	all := svc.List("", "updated")
	if len(all) != 2 {
		t.Errorf("expected 2 notes, got %d", len(all))
	}
}

func TestNoteServiceList_FilterByTag(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	_, _ = svc.Create("note 1", []string{"work"})
	_, _ = svc.Create("note 2", []string{"personal"})
	_, _ = svc.Create("note 3", []string{"work"})

	work := svc.List("work", "updated")
	if len(work) != 2 {
		t.Errorf("expected 2 work notes, got %d", len(work))
	}
}

func TestNoteServiceGet_Found(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	n, _ := svc.Create("find me", nil)

	found, err := svc.Get(n.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found.Text != "find me" {
		t.Errorf("expected text='find me', got %q", found.Text)
	}
}

func TestNoteServiceGet_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()

	if _, err := svc.Get(999); err == nil {
		t.Error("expected error for non-existent id")
	}
}

func TestNoteServiceUpdate_TextOnly(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	n, _ := svc.Create("original", []string{"tag1"})

	text := "updated text"
	updated, err := svc.Update(n.ID, NoteUpdate{Text: &text})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Text != "updated text" {
		t.Errorf("expected text='updated text', got %q", updated.Text)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "tag1" {
		t.Errorf("expected tags unchanged=[tag1], got %v", updated.Tags)
	}
}

func TestNoteServiceUpdate_TagsOnly(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	n, _ := svc.Create("original", []string{"tag1"})

	tags := []string{"newtag"}
	updated, err := svc.Update(n.ID, NoteUpdate{Tags: &tags})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Text != "original" {
		t.Errorf("expected text unchanged, got %q", updated.Text)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "newtag" {
		t.Errorf("expected tags=[newtag], got %v", updated.Tags)
	}
}

func TestNoteServiceUpdate_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()

	text := "x"
	if _, err := svc.Update(999, NoteUpdate{Text: &text}); err == nil {
		t.Error("expected error for non-existent id")
	}
}

func TestNoteServiceDelete(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	n, _ := svc.Create("delete me", nil)

	deleted, err := svc.Delete(n.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted.ID != n.ID {
		t.Errorf("expected deleted id=%d, got %d", n.ID, deleted.ID)
	}
	if len(svc.notes) != 0 {
		t.Errorf("expected 0 notes after delete, got %d", len(svc.notes))
	}
}

func TestNoteServiceDelete_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()

	if _, err := svc.Delete(999); err == nil {
		t.Error("expected error for non-existent id")
	}
}

func TestNoteServiceSearch_Regex(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	_, _ = svc.Create("# Meeting notes\n\nDiscuss project", nil)
	_, _ = svc.Create("Shopping list", nil)
	_, _ = svc.Create("Project plan", nil)

	results := svc.Search("project")
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestNoteServiceSearch_CaseInsensitive(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	_, _ = svc.Create("Hello World", nil)

	results := svc.Search("hello")
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestNoteServiceSearch_InvalidRegex(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	_, _ = svc.Create("test [bracket]", nil)

	results := svc.Search("[bracket")
	if len(results) != 1 {
		t.Errorf("expected 1 result (escaped), got %d", len(results))
	}
}

func TestNoteServiceAllTags(t *testing.T) {
	svc, _ := newTestService(t)
	_ = svc.Load()
	_, _ = svc.Create("n1", []string{"work", "idea"})
	_, _ = svc.Create("n2", []string{"work"})
	_, _ = svc.Create("n3", []string{"personal"})

	tags := svc.AllTags()
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tags))
	}
	if tags[0].Tag != "work" || tags[0].Count != 2 {
		t.Errorf("expected work count=2 first, got %v", tags[0])
	}
}

func TestNoteServiceSaveLoadRoundtrip(t *testing.T) {
	svc, dataFile := newTestService(t)
	_ = svc.Load()
	_, _ = svc.Create("persist me", []string{"tag1"})
	if err := svc.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	svc2 := NewNoteService(dataFile)
	if err := svc2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(svc2.notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(svc2.notes))
	}
	if svc2.notes[0].Text != "persist me" {
		t.Errorf("expected text='persist me', got %q", svc2.notes[0].Text)
	}
	if len(svc2.notes[0].Tags) != 1 || svc2.notes[0].Tags[0] != "tag1" {
		t.Errorf("expected tags=[tag1], got %v", svc2.notes[0].Tags)
	}
}

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
