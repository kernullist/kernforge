package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type PlanItem struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type Session struct {
	ID                       string                  `json:"id"`
	Name                     string                  `json:"name"`
	WorkingDir               string                  `json:"working_dir"`
	Provider                 string                  `json:"provider"`
	Model                    string                  `json:"model"`
	BaseURL                  string                  `json:"base_url,omitempty"`
	PermissionMode           string                  `json:"permission_mode"`
	CreatedAt                time.Time               `json:"created_at"`
	UpdatedAt                time.Time               `json:"updated_at"`
	Summary                  string                  `json:"summary,omitempty"`
	LastAnalysisContextQuery string                  `json:"last_analysis_context_query,omitempty"`
	LastAnalysisContextRunID string                  `json:"last_analysis_context_run_id,omitempty"`
	ActiveFeatureID          string                  `json:"active_feature_id,omitempty"`
	LastFeatureID            string                  `json:"last_feature_id,omitempty"`
	Plan                     []PlanItem              `json:"plan,omitempty"`
	LastAnalysis             *ProjectAnalysisSummary `json:"last_analysis,omitempty"`
	LastVerification         *VerificationReport     `json:"last_verification,omitempty"`
	LastSelection            *ViewerSelection        `json:"last_selection,omitempty"`
	Selections               []ViewerSelection       `json:"selections,omitempty"`
	ActiveSelection          int                     `json:"active_selection,omitempty"`
	Messages                 []Message               `json:"messages"`
}

func NewSession(workingDir, providerName, model, baseURL, permissionMode string) *Session {
	now := time.Now()
	id := fmt.Sprintf("%s-%03d", now.Format("20060102-150405"), now.Nanosecond()/1_000_000)
	return &Session{
		ID:             id,
		Name:           "Session " + id,
		WorkingDir:     workingDir,
		Provider:       providerName,
		Model:          model,
		BaseURL:        baseURL,
		PermissionMode: permissionMode,
		CreatedAt:      now,
		UpdatedAt:      now,
		Messages:       []Message{},
	}
}

func (s *Session) AddMessage(msg Message) {
	s.Messages = append(s.Messages, msg)
	s.UpdatedAt = time.Now()
}

func (s *Session) ApproxChars() int {
	total := len(s.Summary)
	for _, msg := range s.Messages {
		total += len(msg.Text)
		for _, image := range msg.Images {
			total += len(image.Path) + len(image.MediaType)
		}
		total += len(msg.ToolCallID) + len(msg.ToolName)
		for _, tc := range msg.ToolCalls {
			total += len(tc.Name) + len(tc.Arguments)
		}
	}
	return total
}

func (s *Session) ExportText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", s.Name)
	fmt.Fprintf(&b, "Session ID: %s\n", s.ID)
	fmt.Fprintf(&b, "Working dir: %s\n", s.WorkingDir)
	fmt.Fprintf(&b, "Provider/model: %s / %s\n", s.Provider, s.Model)
	if strings.TrimSpace(s.BaseURL) != "" {
		fmt.Fprintf(&b, "Base URL: %s\n", s.BaseURL)
	}
	fmt.Fprintf(&b, "Permission mode: %s\n\n", s.PermissionMode)
	if strings.TrimSpace(s.Summary) != "" {
		b.WriteString("## Summary\n\n")
		b.WriteString(s.Summary)
		b.WriteString("\n\n")
	}
	if len(s.Plan) > 0 {
		b.WriteString("## Plan\n\n")
		for _, item := range s.Plan {
			fmt.Fprintf(&b, "- [%s] %s\n", item.Status, item.Step)
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(s.ActiveFeatureID) != "" {
		b.WriteString("## Active Feature\n\n")
		fmt.Fprintf(&b, "- Active feature: %s\n", s.ActiveFeatureID)
		if strings.TrimSpace(s.LastFeatureID) != "" && !strings.EqualFold(s.LastFeatureID, s.ActiveFeatureID) {
			fmt.Fprintf(&b, "- Last feature: %s\n", s.LastFeatureID)
		}
		b.WriteString("\n")
	}
	if s.LastAnalysis != nil {
		b.WriteString("## Last Analysis\n\n")
		fmt.Fprintf(&b, "- Run ID: %s\n", s.LastAnalysis.RunID)
		fmt.Fprintf(&b, "- Goal: %s\n", s.LastAnalysis.Goal)
		if strings.TrimSpace(s.LastAnalysis.Mode) != "" {
			fmt.Fprintf(&b, "- Mode: %s\n", s.LastAnalysis.Mode)
		}
		fmt.Fprintf(&b, "- Status: %s\n", s.LastAnalysis.Status)
		fmt.Fprintf(&b, "- Agents: %d\n", s.LastAnalysis.AgentCount)
		if strings.TrimSpace(s.LastAnalysis.OutputPath) != "" {
			fmt.Fprintf(&b, "- Output: %s\n", s.LastAnalysis.OutputPath)
		}
		b.WriteString("\n")
	}
	if s.LastVerification != nil {
		b.WriteString("## Last Verification\n\n")
		b.WriteString(s.LastVerification.RenderDetailed())
		b.WriteString("\n\n")
	}
	if s.LastSelection != nil && s.LastSelection.HasSelection() {
		b.WriteString("## Last Selection\n\n")
		fmt.Fprintf(&b, "- %s\n\n", s.LastSelection.Summary(s.WorkingDir))
	}
	if len(s.Selections) > 0 {
		b.WriteString("## Selection Stack\n\n")
		for i, selection := range s.Selections {
			active := ""
			if i == s.ActiveSelection {
				active = " [active]"
			}
			fmt.Fprintf(&b, "- %d. %s%s\n", i+1, selection.Summary(s.WorkingDir), active)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Transcript\n\n")
	for _, msg := range s.Messages {
		fmt.Fprintf(&b, "### %s\n\n", strings.ToUpper(msg.Role))
		if msg.ToolName != "" {
			fmt.Fprintf(&b, "Tool: %s (%s)\n\n", msg.ToolName, msg.ToolCallID)
		}
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				fmt.Fprintf(&b, "- tool_call: %s %s\n", tc.Name, tc.Arguments)
			}
			b.WriteString("\n")
		}
		if len(msg.Images) > 0 {
			for _, image := range msg.Images {
				fmt.Fprintf(&b, "- image: %s (%s)\n", image.Path, image.MediaType)
			}
			b.WriteString("\n")
		}
		if msg.Text != "" {
			b.WriteString(msg.Text)
			if !strings.HasSuffix(msg.Text, "\n") {
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

type SessionSummary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	WorkingDir string    `json:"working_dir"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type SessionStore struct {
	root string
}

func NewSessionStore(root string) *SessionStore {
	return &SessionStore{root: root}
}

func (s *SessionStore) Root() string {
	return s.root
}

func (s *SessionStore) Save(sess *Session) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	sess.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.root, sess.ID+".json"), data, 0o644)
}

func (s *SessionStore) Load(id string) (*Session, error) {
	data, err := os.ReadFile(filepath.Join(s.root, id+".json"))
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	sess.normalizeSelectionState()
	return &sess, nil
}

func (s *SessionStore) List() ([]SessionSummary, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := make([]SessionSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.root, entry.Name()))
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		items = append(items, SessionSummary{
			ID:         sess.ID,
			Name:       sess.Name,
			WorkingDir: sess.WorkingDir,
			UpdatedAt:  sess.UpdatedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *Session) normalizeSelectionState() {
	if len(s.Selections) == 0 {
		if s.LastSelection != nil && s.LastSelection.HasSelection() {
			s.Selections = []ViewerSelection{*s.LastSelection}
			s.ActiveSelection = 0
		}
		return
	}
	filtered := make([]ViewerSelection, 0, len(s.Selections))
	for _, selection := range s.Selections {
		if selection.HasSelection() {
			filtered = append(filtered, selection)
		}
	}
	s.Selections = filtered
	if len(s.Selections) == 0 {
		s.LastSelection = nil
		s.ActiveSelection = 0
		return
	}
	if s.ActiveSelection < 0 || s.ActiveSelection >= len(s.Selections) {
		s.ActiveSelection = len(s.Selections) - 1
	}
	active := s.Selections[s.ActiveSelection]
	s.LastSelection = &active
}

func (s *Session) CurrentSelection() *ViewerSelection {
	s.normalizeSelectionState()
	if len(s.Selections) == 0 {
		return nil
	}
	selection := s.Selections[s.ActiveSelection]
	return &selection
}

func (s *Session) AddSelection(selection ViewerSelection) {
	if !selection.HasSelection() {
		return
	}
	s.normalizeSelectionState()
	for i, existing := range s.Selections {
		if strings.EqualFold(existing.FilePath, selection.FilePath) && existing.StartLine == selection.StartLine && existing.EndLine == selection.EndLine {
			if strings.TrimSpace(selection.Note) != "" {
				s.Selections[i].Note = selection.Note
			}
			if len(selection.Tags) > 0 {
				s.Selections[i].Tags = uniqueStrings(selection.Tags)
			}
			s.ActiveSelection = i
			s.LastSelection = &s.Selections[i]
			return
		}
	}
	s.Selections = append(s.Selections, selection)
	s.ActiveSelection = len(s.Selections) - 1
	active := s.Selections[s.ActiveSelection]
	s.LastSelection = &active
}

func (s *Session) SetActiveSelection(index int) bool {
	s.normalizeSelectionState()
	if index < 0 || index >= len(s.Selections) {
		return false
	}
	s.ActiveSelection = index
	active := s.Selections[index]
	s.LastSelection = &active
	return true
}

func (s *Session) RemoveSelection(index int) bool {
	s.normalizeSelectionState()
	if index < 0 || index >= len(s.Selections) {
		return false
	}
	s.Selections = append(s.Selections[:index], s.Selections[index+1:]...)
	if len(s.Selections) == 0 {
		s.LastSelection = nil
		s.ActiveSelection = 0
		return true
	}
	if s.ActiveSelection >= len(s.Selections) {
		s.ActiveSelection = len(s.Selections) - 1
	}
	active := s.Selections[s.ActiveSelection]
	s.LastSelection = &active
	return true
}

func (s *Session) ClearSelections() {
	s.Selections = nil
	s.ActiveSelection = 0
	s.LastSelection = nil
}

func (s *Session) SelectionAt(index int) (*ViewerSelection, bool) {
	s.normalizeSelectionState()
	if index < 0 || index >= len(s.Selections) {
		return nil, false
	}
	selection := s.Selections[index]
	return &selection, true
}
