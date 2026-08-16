package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxTextLength = 100 * 1024
	maxTags       = 20
	maxTagLength  = 60
)

// Note is a single persisted note.
type Note struct {
	ID        int      `json:"id"`
	Text      string   `json:"text"`
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
}

// NoteService manages in-memory notes with JSON file persistence.
type NoteService struct {
	dataFile string
	notes    []Note
	nextID   int
}

// NewNoteService creates a service backed by dataFile.
func NewNoteService(dataFile string) *NoteService {
	return &NoteService{dataFile: dataFile, nextID: 1}
}

// Load reads notes from the data file. Missing file is not an error.
func (s *NoteService) Load() error {
	raw, err := os.ReadFile(s.dataFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read notes: %w", err)
	}

	var data struct {
		Notes []Note `json:"notes"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("parse notes: %w", err)
	}

	s.notes = make([]Note, 0, len(data.Notes))
	maxID := 0
	for _, n := range data.Notes {
		n = migrateNote(n)
		if n.ID > maxID {
			maxID = n.ID
		}
		s.notes = append(s.notes, n)
	}
	s.nextID = maxID + 1
	return nil
}

// Save writes notes to the data file, creating parent dirs as needed.
func (s *NoteService) Save() error {
	dir := filepath.Dir(s.dataFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	data := struct {
		Notes []Note `json:"notes"`
	}{Notes: s.notes}

	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notes: %w", err)
	}
	if err := os.WriteFile(s.dataFile, raw, 0o644); err != nil {
		return fmt.Errorf("write notes: %w", err)
	}
	return nil
}

func migrateNote(n Note) Note {
	if n.Tags == nil {
		n.Tags = []string{}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if n.CreatedAt == "" {
		n.CreatedAt = now
	}
	if n.UpdatedAt == "" {
		n.UpdatedAt = n.CreatedAt
	}
	return n
}

func validateText(text string) error {
	if text == "" {
		return fmt.Errorf("text must not be empty")
	}
	if len(text) > maxTextLength {
		return fmt.Errorf("text too long (%d chars, max %d)", len(text), maxTextLength)
	}
	return nil
}

func validateTags(tags []string) error {
	if len(tags) > maxTags {
		return fmt.Errorf("too many tags (max %d)", maxTags)
	}
	for _, tag := range tags {
		if tag == "" {
			return fmt.Errorf("each tag must be a non-empty string")
		}
		if len(tag) > maxTagLength {
			return fmt.Errorf("tag too long (max %d chars): %s...", maxTagLength, tag[:min(20, len(tag))])
		}
	}
	return nil
}

// Create adds a new note and returns it.
func (s *NoteService) Create(text string, tags []string) (Note, error) {
	if err := validateText(text); err != nil {
		return Note{}, err
	}
	if err := validateTags(tags); err != nil {
		return Note{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	n := Note{
		ID:        s.nextID,
		Text:      text,
		Tags:      tags,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.nextID++
	s.notes = append(s.notes, n)
	return n, nil
}

// List returns notes optionally filtered by tag, sorted by updatedAt (default)
// or createdAt.
func (s *NoteService) List(tag, sortKey string) []Note {
	var out []Note
	for _, n := range s.notes {
		if tag != "" {
			found := false
			for _, t := range n.Tags {
				if t == tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, n)
	}

	key := "updatedAt"
	if sortKey == "created" {
		key = "createdAt"
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		aVal := fieldByPath(a, key)
		bVal := fieldByPath(b, key)
		return aVal > bVal // newest first
	})

	return out
}

func fieldByPath(n Note, key string) string {
	switch key {
	case "createdAt":
		return n.CreatedAt
	default:
		return n.UpdatedAt
	}
}

// Get returns a note by ID.
func (s *NoteService) Get(id int) (Note, error) {
	for _, n := range s.notes {
		if n.ID == id {
			return n, nil
		}
	}
	return Note{}, fmt.Errorf("Note not found: %d", id)
}

// NoteUpdate holds optional fields for Update.
type NoteUpdate struct {
	Text *string
	Tags *[]string
}

// Update modifies a note's text and/or tags.
func (s *NoteService) Update(id int, updates NoteUpdate) (Note, error) {
	idx := -1
	for i, n := range s.notes {
		if n.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return Note{}, fmt.Errorf("Note not found: %d", id)
	}

	n := s.notes[idx]
	if updates.Text != nil {
		if err := validateText(*updates.Text); err != nil {
			return Note{}, err
		}
		n.Text = *updates.Text
	}
	if updates.Tags != nil {
		if err := validateTags(*updates.Tags); err != nil {
			return Note{}, err
		}
		n.Tags = *updates.Tags
	}
	n.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.notes[idx] = n
	return n, nil
}

// Delete removes a note by ID and returns the deleted note.
func (s *NoteService) Delete(id int) (Note, error) {
	for i, n := range s.notes {
		if n.ID == id {
			s.notes = append(s.notes[:i], s.notes[i+1:]...)
			return n, nil
		}
	}
	return Note{}, fmt.Errorf("Note not found: %d", id)
}

// Search returns notes whose text matches a case-insensitive regex pattern.
// Invalid patterns are escaped and retried.
func (s *NoteService) Search(query string) []Note {
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		escaped := regexp.QuoteMeta(query)
		re, err = regexp.Compile("(?i)" + escaped)
		if err != nil {
			return nil
		}
	}

	var out []Note
	for _, n := range s.notes {
		if re.MatchString(n.Text) {
			out = append(out, n)
		}
	}
	return out
}

// AllTags returns tag counts sorted by frequency (descending).
func (s *NoteService) AllTags() []TagCount {
	counts := map[string]int{}
	for _, n := range s.notes {
		for _, t := range n.Tags {
			counts[t]++
		}
	}
	out := make([]TagCount, 0, len(counts))
	for tag, count := range counts {
		out = append(out, TagCount{Tag: tag, Count: count})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.Compare(out[i].Tag, out[j].Tag) < 0
	})
	return out
}

// TagCount is a tag name with its usage count.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// Total returns the number of stored notes.
func (s *NoteService) Total() int {
	return len(s.notes)
}
