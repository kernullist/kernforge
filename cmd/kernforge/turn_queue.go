package main

import (
	"fmt"
	"strings"
	"time"
)

type QueueKind string

const (
	QueueKindUserSteer   QueueKind = "user_steer"
	QueueKindFollowUp    QueueKind = "follow_up"
	QueueKindAside       QueueKind = "aside"
	QueueKindMaintenance QueueKind = "maintenance"
)

type TurnQueueItem struct {
	ID            string         `json:"id,omitempty"`
	Kind          QueueKind      `json:"kind"`
	Text          string         `json:"text"`
	SourceText    string         `json:"source_text,omitempty"`
	Images        []MessageImage `json:"images,omitempty"`
	ActiveRequest string         `json:"active_request,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	CreatedAt     time.Time      `json:"created_at,omitempty"`
}

func NewTurnQueueItem(active RequestEnvelope, userText string, images []MessageImage, now time.Time) TurnQueueItem {
	if now.IsZero() {
		now = time.Now()
	}
	kind, reason := ClassifyTurnQueueKind(active, userText)
	text := strings.TrimSpace(userText)
	return TurnQueueItem{
		ID:            fmt.Sprintf("turnq-%d", now.UnixNano()),
		Kind:          kind,
		Text:          text,
		SourceText:    text,
		Images:        append([]MessageImage(nil), images...),
		ActiveRequest: strings.TrimSpace(active.ExternalUserText),
		Reason:        reason,
		CreatedAt:     now,
	}
}

func ClassifyTurnQueueKind(active RequestEnvelope, userText string) (QueueKind, string) {
	text := strings.TrimSpace(baseUserQueryText(userText))
	if text == "" {
		text = strings.TrimSpace(userText)
	}
	lower := strings.ToLower(text)
	if lower == "" {
		return QueueKindFollowUp, "empty queued input defaults to follow-up"
	}
	if queuedTextLooksLikeSteering(lower) {
		return QueueKindUserSteer, "input changes direction or constraints for the next turn"
	}
	if queuedTextLooksLikeMaintenance(lower) {
		return QueueKindMaintenance, "input asks the agent to continue or resume maintenance"
	}
	if queuedTextLooksLikeAside(lower) {
		return QueueKindAside, "input is framed as an aside for later context"
	}
	return QueueKindFollowUp, "input is a follow-up to the active request"
}

func queuedTextLooksLikeSteering(lower string) bool {
	return containsAny(lower,
		"하지 말고",
		"하지마",
		"멈춰",
		"중단",
		"취소",
		"아니",
		"그게 아니라",
		"대신",
		"우선",
		"먼저",
		"방향",
		"바꿔",
		"수정해서",
		"다르게",
		"stop",
		"cancel",
		"abort",
		"instead",
		"rather than",
		"change direction",
		"prioritize",
		"do not",
		"don't",
	)
}

func queuedTextLooksLikeMaintenance(lower string) bool {
	trimmed := strings.TrimSpace(lower)
	if containsAny(trimmed,
		"계속",
		"이어",
		"마저",
		"진행해",
		"진행하자",
		"resume",
		"continue",
		"keep going",
		"carry on",
	) {
		return true
	}
	return trimmed == "go on" || trimmed == "next"
}

func queuedTextLooksLikeAside(lower string) bool {
	trimmed := strings.TrimSpace(lower)
	return strings.HasPrefix(trimmed, "aside:") ||
		strings.HasPrefix(trimmed, "btw") ||
		strings.HasPrefix(trimmed, "by the way") ||
		strings.HasPrefix(trimmed, "참고로") ||
		strings.HasPrefix(trimmed, "그나저나") ||
		strings.HasPrefix(trimmed, "별개로") ||
		strings.HasPrefix(trimmed, "덧붙여") ||
		strings.HasPrefix(trimmed, "(")
}

func (s *Session) EnqueueTurnInput(active RequestEnvelope, userText string, images []MessageImage) TurnQueueItem {
	item := NewTurnQueueItem(active, userText, images, time.Now())
	if s == nil {
		return item
	}
	s.TurnQueue = append(s.TurnQueue, item)
	s.UpdatedAt = time.Now()
	return item
}

func (s *Session) PopTurnQueue() (TurnQueueItem, bool) {
	if s == nil || len(s.TurnQueue) == 0 {
		return TurnQueueItem{}, false
	}
	item := s.TurnQueue[0]
	copy(s.TurnQueue, s.TurnQueue[1:])
	s.TurnQueue = s.TurnQueue[:len(s.TurnQueue)-1]
	s.UpdatedAt = time.Now()
	return item, true
}

func (s *Session) HasTurnQueue() bool {
	return s != nil && len(s.TurnQueue) > 0
}

func (a *Agent) EnqueueUserInputDuringExecution(userText string, images []MessageImage) (TurnQueueItem, error) {
	if a == nil || a.Session == nil {
		return TurnQueueItem{}, fmt.Errorf("no active session")
	}
	active := a.latestRequestEnvelopeFor(sessionEffectiveUserRequestText(a.Session))
	item := a.Session.EnqueueTurnInput(active, userText, images)
	if a.Store != nil {
		if err := a.Store.Save(a.Session); err != nil {
			return TurnQueueItem{}, err
		}
	}
	return item, nil
}

func formatQueuedTurnInputReply(cfg Config, item TurnQueueItem) string {
	kind := strings.TrimSpace(string(item.Kind))
	if kind == "" {
		kind = string(QueueKindFollowUp)
	}
	if localePrefersKorean(cfg) {
		return fmt.Sprintf("현재 실행 중인 요청을 오염시키지 않도록 입력을 다음 turn queue에 저장했습니다. kind=%s", kind)
	}
	return fmt.Sprintf("Queued this input for the next turn without modifying the active request. kind=%s", kind)
}
