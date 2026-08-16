package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/google/uuid"
)

var defaultColumns = []string{"Backlog", "Todo", "In Progress", "Review", "Done"}

var sessionColors = []string{
	"#3B82F6", "#10B981", "#8B5CF6", "#F59E0B", "#EF4444",
	"#EC4899", "#06B6D4", "#84CC16", "#F97316", "#6366F1",
}

// Priority levels for tickets.
type Priority string

const (
	PriorityUrgent Priority = "urgent"
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

// Project is a Kanban project.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// Column is a workflow column within a project.
type Column struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	Order     int    `json:"order"`
}

// Session is an agent session for filtering board work.
type Session struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	Branch     *string `json:"branch"`
	IsWorktree bool   `json:"is_worktree"`
	CreatedAt  string `json:"created_at"`
}

// Ticket is a story or subtask on the board.
type Ticket struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	TicketNumber   int       `json:"ticket_number"`
	Title          string    `json:"title"`
	Description    *string   `json:"description"`
	Priority       *Priority `json:"priority"`
	ColumnID       string    `json:"column_id"`
	SessionID      *string   `json:"session_id"`
	ParentTicketID *string   `json:"parent_ticket_id"`
	Order          int       `json:"order"`
	CreatedAt      string    `json:"created_at"`
	UpdatedAt      string    `json:"updated_at"`
}

// TicketWithSubtasks is a ticket with its subtask list and progress.
type TicketWithSubtasks struct {
	Ticket
	Subtasks        []Ticket `json:"subtasks"`
	SubtaskTotal    int      `json:"subtask_total"`
	SubtaskCompleted int     `json:"subtask_completed"`
}

// Store is the JSON-backed Kanban data store.
type Store struct {
	dataFile string
	data     storeData
}

type storeData struct {
	Projects []Project `json:"projects"`
	Columns  []Column  `json:"columns"`
	Sessions []Session `json:"sessions"`
	Tickets  []Ticket  `json:"tickets"`
}

// NewStore creates a store backed by dataFile.
func NewStore(dataFile string) *Store {
	return &Store{dataFile: dataFile}
}

// Load reads the store from disk. Missing file is not an error.
func (s *Store) Load() {
	s.data = storeData{}
	raw, err := os.ReadFile(s.dataFile)
	if err != nil {
		if !os.IsNotExist(err) {
			stderr("failed to load kanban: %s", err)
		}
		return
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		stderr("failed to parse kanban: %s", err)
		s.data = storeData{}
	}
}

// Save writes the store to disk.
func (s *Store) Save() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(s.dataFile, raw, 0o644)
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func (s *Store) saveOrLog() {
	if err := s.Save(); err != nil {
		stderr("save failed: %s", err)
	}
}

// --- Projects ---

func (s *Store) CreateProject(name string) Project {
	id := uuid.NewString()
	ts := now()
	p := Project{ID: id, Name: name, CreatedAt: ts}
	s.data.Projects = append(s.data.Projects, p)

	for i, colName := range defaultColumns {
		s.data.Columns = append(s.data.Columns, Column{
			ID:        uuid.NewString(),
			ProjectID: id,
			Name:      colName,
			Order:     i,
		})
	}

	s.saveOrLog()
	return p
}

func (s *Store) GetProject(id string) *Project {
	for i := range s.data.Projects {
		if s.data.Projects[i].ID == id {
			return &s.data.Projects[i]
		}
	}
	return nil
}

func (s *Store) ListProjects() []Project {
	out := make([]Project, len(s.data.Projects))
	copy(out, s.data.Projects)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

func (s *Store) GetOrCreateDefaultProject() Project {
	projects := s.ListProjects()
	if len(projects) > 0 {
		return projects[0]
	}
	return s.CreateProject("Default Project")
}

// --- Columns ---

func (s *Store) GetColumns(projectID string) []Column {
	var out []Column
	for _, c := range s.data.Columns {
		if c.ProjectID == projectID {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Order < out[j].Order
	})
	return out
}

func (s *Store) GetColumn(id string) *Column {
	for i := range s.data.Columns {
		if s.data.Columns[i].ID == id {
			return &s.data.Columns[i]
		}
	}
	return nil
}

func (s *Store) GetBacklogColumn(projectID string) *Column {
	for i := range s.data.Columns {
		if s.data.Columns[i].ProjectID == projectID && s.data.Columns[i].Name == "Backlog" {
			return &s.data.Columns[i]
		}
	}
	return nil
}

func (s *Store) GetDoneColumn(projectID string) *Column {
	for i := range s.data.Columns {
		if s.data.Columns[i].ProjectID == projectID && s.data.Columns[i].Name == "Done" {
			return &s.data.Columns[i]
		}
	}
	return nil
}

// --- Sessions ---

func (s *Store) CreateSession(name string, color *string) Session {
	id := uuid.NewString()
	all := s.ListSessions()
	if color == nil || *color == "" {
		c := sessionColors[len(all)%len(sessionColors)]
		color = &c
	}
	sess := Session{
		ID:        id,
		Name:      name,
		Color:     *color,
		Branch:    nil,
		CreatedAt: now(),
	}
	s.data.Sessions = append(s.data.Sessions, sess)
	s.saveOrLog()
	return sess
}

func (s *Store) GetSession(id string) *Session {
	for i := range s.data.Sessions {
		if s.data.Sessions[i].ID == id {
			return &s.data.Sessions[i]
		}
	}
	return nil
}

func (s *Store) ListSessions() []Session {
	out := make([]Session, len(s.data.Sessions))
	copy(out, s.data.Sessions)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

func (s *Store) DeleteSession(id string) bool {
	sess := s.GetSession(id)
	if sess == nil {
		return false
	}

	ts := now()
	for i := range s.data.Tickets {
		if s.data.Tickets[i].SessionID != nil && *s.data.Tickets[i].SessionID == id {
			s.data.Tickets[i].SessionID = nil
			s.data.Tickets[i].UpdatedAt = ts
		}
	}

	for i := range s.data.Sessions {
		if s.data.Sessions[i].ID == id {
			s.data.Sessions = append(s.data.Sessions[:i], s.data.Sessions[i+1:]...)
			break
		}
	}

	s.saveOrLog()
	return true
}

// --- Tickets ---

func (s *Store) nextTicketNumber(projectID string) int {
	max := 0
	for _, t := range s.data.Tickets {
		if t.ProjectID == projectID && t.TicketNumber > max {
			max = t.TicketNumber
		}
	}
	return max + 1
}

func (s *Store) maxOrderInColumn(columnID string) int {
	max := -1
	for _, t := range s.data.Tickets {
		if t.ColumnID == columnID && t.Order > max {
			max = t.Order
		}
	}
	return max + 1
}

// CreateTicketInput holds parameters for creating a ticket.
type CreateTicketInput struct {
	Title       string
	Description *string
	ProjectID   string
	SessionID   *string
	Priority    *Priority
	ColumnID    *string
}

func (s *Store) CreateTicket(input CreateTicketInput) (Ticket, error) {
	columnID := ""
	if input.ColumnID != nil && *input.ColumnID != "" {
		columnID = *input.ColumnID
	} else {
		backlog := s.GetBacklogColumn(input.ProjectID)
		if backlog == nil {
			return Ticket{}, fmt.Errorf("No Backlog column found")
		}
		columnID = backlog.ID
	}

	t := Ticket{
		ID:           uuid.NewString(),
		ProjectID:    input.ProjectID,
		TicketNumber: s.nextTicketNumber(input.ProjectID),
		Title:        input.Title,
		Description:  input.Description,
		Priority:     input.Priority,
		ColumnID:     columnID,
		SessionID:    input.SessionID,
		Order:        s.maxOrderInColumn(columnID),
		CreatedAt:    now(),
		UpdatedAt:    now(),
	}
	s.data.Tickets = append(s.data.Tickets, t)
	s.saveOrLog()
	return t, nil
}

func (s *Store) CreateSubtask(parentTicketID, title string, description *string, priority *Priority) (Ticket, error) {
	parent := s.GetTicket(parentTicketID)
	if parent == nil {
		return Ticket{}, fmt.Errorf("Parent ticket not found")
	}

	t := Ticket{
		ID:             uuid.NewString(),
		ProjectID:      parent.ProjectID,
		TicketNumber:   s.nextTicketNumber(parent.ProjectID),
		Title:          title,
		Description:    description,
		Priority:       priority,
		ColumnID:       parent.ColumnID,
		SessionID:      parent.SessionID,
		ParentTicketID: &parentTicketID,
		Order:          s.maxOrderInColumn(parent.ColumnID),
		CreatedAt:      now(),
		UpdatedAt:      now(),
	}
	s.data.Tickets = append(s.data.Tickets, t)
	s.saveOrLog()
	return t, nil
}

func (s *Store) GetTicket(id string) *Ticket {
	for i := range s.data.Tickets {
		if s.data.Tickets[i].ID == id {
			return &s.data.Tickets[i]
		}
	}
	return nil
}

func (s *Store) GetTicketWithSubtasks(id string) *TicketWithSubtasks {
	t := s.GetTicket(id)
	if t == nil {
		return nil
	}
	subs := s.GetSubtasks(id)
	progress := s.GetStoryProgress(id)
	return &TicketWithSubtasks{
		Ticket:          *t,
		Subtasks:        subs,
		SubtaskTotal:    progress.Total,
		SubtaskCompleted: progress.Completed,
	}
}

// TicketFilters holds optional filters for ListTickets.
type TicketFilters struct {
	ProjectID      string
	ColumnID       string
	SessionID      string
	ParentTicketID *string
}

func (s *Store) ListTickets(filters TicketFilters) []Ticket {
	var out []Ticket
	for _, t := range s.data.Tickets {
		if filters.ProjectID != "" && t.ProjectID != filters.ProjectID {
			continue
		}
		if filters.ColumnID != "" && t.ColumnID != filters.ColumnID {
			continue
		}
		if filters.SessionID != "" {
			if t.SessionID == nil || *t.SessionID != filters.SessionID {
				continue
			}
		}
		if filters.ParentTicketID != nil {
			if *filters.ParentTicketID == "" {
				if t.ParentTicketID != nil {
					continue
				}
			} else {
				if t.ParentTicketID == nil || *t.ParentTicketID != *filters.ParentTicketID {
					continue
				}
			}
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Order < out[j].Order
	})
	return out
}

// UpdateTicketInput holds optional fields for UpdateTicket.
type UpdateTicketInput struct {
	Title       *string
	Description *string
	Priority    *Priority
	SessionID   *string
}

func (s *Store) UpdateTicket(id string, updates UpdateTicketInput) *Ticket {
	t := s.GetTicket(id)
	if t == nil {
		return nil
	}
	if updates.Title != nil {
		t.Title = *updates.Title
	}
	if updates.Description != nil {
		t.Description = updates.Description
	}
	if updates.Priority != nil {
		t.Priority = updates.Priority
	}
	if updates.SessionID != nil {
		t.SessionID = updates.SessionID
	}
	t.UpdatedAt = now()
	s.saveOrLog()
	return t
}

func (s *Store) MoveTicket(id, columnID string, order *int) *Ticket {
	t := s.GetTicket(id)
	if t == nil {
		return nil
	}
	t.ColumnID = columnID
	if order != nil {
		t.Order = *order
	} else {
		t.Order = s.maxOrderInColumn(columnID)
	}
	t.UpdatedAt = now()
	s.saveOrLog()
	return t
}

func (s *Store) DeleteTicket(id string) bool {
	toDelete := map[string]bool{id: true}
	for _, t := range s.data.Tickets {
		if t.ParentTicketID != nil && *t.ParentTicketID == id {
			toDelete[t.ID] = true
		}
	}

	before := len(s.data.Tickets)
	var remaining []Ticket
	for _, t := range s.data.Tickets {
		if !toDelete[t.ID] {
			remaining = append(remaining, t)
		}
	}
	s.data.Tickets = remaining
	s.saveOrLog()
	return len(s.data.Tickets) < before
}

func (s *Store) GetSubtasks(parentTicketID string) []Ticket {
	var out []Ticket
	for _, t := range s.data.Tickets {
		if t.ParentTicketID != nil && *t.ParentTicketID == parentTicketID {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Order < out[j].Order
	})
	return out
}

// StoryProgress is the completion status of a story's subtasks.
type StoryProgress struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
}

func (s *Store) GetStoryProgress(ticketID string) StoryProgress {
	subs := s.GetSubtasks(ticketID)
	doneIDs := map[string]bool{}
	for _, c := range s.data.Columns {
		if c.Name == "Done" {
			doneIDs[c.ID] = true
		}
	}
	completed := 0
	for _, sub := range subs {
		if doneIDs[sub.ColumnID] {
			completed++
		}
	}
	return StoryProgress{Total: len(subs), Completed: completed}
}
