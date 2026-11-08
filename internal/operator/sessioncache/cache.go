// Package sessioncache owns the bounded TUI event window for one session.
package sessioncache

import (
	"encoding/json"
	"sort"

	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

// Cache is a non-authoritative frontend projection. It is intentionally not
// synchronized internally; the owning terminal or GUI view serializes access.
type Cache struct {
	sessionID string
	tail      []sessionv1.EventView
	older     []sessionv1.EventView
	tailMore  bool
	olderMore bool
	watermark int64

	state        sessionv1.SessionState
	needsResync  bool
	running      bool
	runningKnown bool
	turnID       string
}

func New() *Cache {
	return &Cache{}
}

func (c *Cache) SessionID() string {
	return c.sessionID
}

func (c *Cache) Watermark() int64 {
	return c.watermark
}

func (c *Cache) State() sessionv1.SessionState {
	return c.state
}

func (c *Cache) NeedsResync() bool {
	return c.needsResync
}

func (c *Cache) TailMore() bool {
	return c.tailMore
}

func (c *Cache) OlderMore() bool {
	return c.olderMore
}

// TurnState reports the last observed turn state. Known is false until a live
// turn.* event has been applied; a process snapshot is not enough to infer it.
func (c *Cache) TurnState() (running, known bool, turnID string) {
	return c.running, c.runningKnown, c.turnID
}

// Window returns the bounded older+tail event window in ascending seq order.
func (c *Cache) Window() []sessionv1.EventView {
	return canonicalEvents(append(append([]sessionv1.EventView{}, c.older...), c.tail...))
}

func (c *Cache) WindowFirstSeq() int64 {
	window := c.Window()
	if len(window) == 0 {
		return 0
	}
	return window[0].Seq
}

// ApplySnapshot replaces the latest tail while preserving older pages that are
// still strictly before the new tail. This prevents a refresh from jumping the
// user back to the newest page.
func (c *Cache) ApplySnapshot(snapshot sessionv1.SessionSnapshotResponse) bool {
	if snapshot.SessionID == "" || (c.sessionID != "" && c.sessionID != snapshot.SessionID) {
		if snapshot.SessionID == "" {
			return false
		}
		c.resetForSession(snapshot.SessionID)
	}
	previousTail := c.tail
	c.sessionID = snapshot.SessionID
	c.tail = canonicalEvents(snapshot.Events)
	if len(c.tail) == 0 {
		c.older = nil
		c.olderMore = false
	} else {
		retained := append(append([]sessionv1.EventView{}, c.older...), previousTail...)
		c.older = eventsBefore(retained, c.tail[0].Seq)
		c.olderMore = c.olderMore || (len(c.older) > 0 && c.older[0].Seq > 1)
	}
	c.tailMore = snapshot.HasMoreBefore
	c.watermark = snapshot.Watermark
	c.state = snapshot.State
	c.needsResync = false
	c.running = false
	c.runningKnown = false
	c.turnID = ""
	return true
}

// ApplyHistory prepends one older page and de-duplicates the seam against the
// current tail.
func (c *Cache) ApplyHistory(response sessionv1.SessionHistoryResponse) bool {
	if response.SessionID == "" || c.sessionID != response.SessionID || response.ResyncRequired {
		if response.ResyncRequired {
			c.needsResync = true
		}
		return false
	}
	merged := canonicalEvents(append(append([]sessionv1.EventView{}, c.older...), response.Events...))
	if len(c.tail) > 0 {
		merged = eventsBefore(merged, c.tail[0].Seq)
	}
	c.older = merged
	c.olderMore = response.HasMoreBefore
	if response.Watermark > c.watermark {
		c.watermark = response.Watermark
	}
	c.needsResync = false
	return true
}

// ApplyIncremental appends catch-up/live events whose seq is greater than the
// current tail. A sequence hole marks the cache stale so the owner must refresh
// from a snapshot instead of guessing missing events.
func (c *Cache) ApplyIncremental(response sessionv1.EventsResponse) bool {
	if response.ResyncRequired {
		c.needsResync = true
		return false
	}
	events := canonicalEvents(response.Events)
	lastSeq := int64(0)
	if len(c.tail) > 0 {
		lastSeq = c.tail[len(c.tail)-1].Seq
	}
	applied := make([]sessionv1.EventView, 0, len(events))
	for _, event := range events {
		if event.Seq <= lastSeq {
			continue
		}
		if lastSeq > 0 && event.Seq != lastSeq+1 {
			c.needsResync = true
			return false
		}
		applied = append(applied, event)
		lastSeq = event.Seq
	}
	if len(applied) > 0 {
		c.tail = append(c.tail, applied...)
		c.applyTurnEvents(applied)
	}
	if response.Watermark > c.watermark {
		c.watermark = response.Watermark
	}
	if response.Cursor > c.watermark {
		c.watermark = response.Cursor
	}
	return len(applied) > 0
}

func (c *Cache) ClearOlder() {
	c.older = nil
	c.olderMore = false
}

func (c *Cache) resetForSession(sessionID string) {
	c.sessionID = sessionID
	c.tail = nil
	c.older = nil
	c.tailMore = false
	c.olderMore = false
	c.watermark = 0
	c.state = ""
	c.needsResync = false
	c.running = false
	c.runningKnown = false
	c.turnID = ""
}

func (c *Cache) applyTurnEvents(events []sessionv1.EventView) {
	for _, event := range events {
		turnID := eventTurnID(event.Payload)
		switch event.Type {
		case "turn.started":
			c.running = true
			c.runningKnown = true
			c.turnID = turnID
		case "turn.completed":
			if turnID == "" || c.turnID == "" || c.turnID == turnID {
				c.running = false
				c.runningKnown = true
				c.turnID = ""
			}
		}
	}
}

func eventTurnID(payload json.RawMessage) string {
	var value struct {
		TurnID string `json:"turn_id"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return ""
	}
	return value.TurnID
}

func canonicalEvents(events []sessionv1.EventView) []sessionv1.EventView {
	if len(events) == 0 {
		return nil
	}
	result := append([]sessionv1.EventView{}, events...)
	sort.SliceStable(result, func(i, j int) bool { return result[i].Seq < result[j].Seq })
	deduplicated := result[:0]
	lastSeq := int64(-1)
	for _, event := range result {
		if event.Seq == lastSeq {
			continue
		}
		deduplicated = append(deduplicated, event)
		lastSeq = event.Seq
	}
	return deduplicated
}

func eventsBefore(events []sessionv1.EventView, seq int64) []sessionv1.EventView {
	filtered := make([]sessionv1.EventView, 0, len(events))
	for _, event := range events {
		if event.Seq < seq {
			filtered = append(filtered, event)
		}
	}
	return canonicalEvents(filtered)
}
