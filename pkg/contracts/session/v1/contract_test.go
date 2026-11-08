package sessionv1

import (
	"encoding/json"
	"testing"
)

func TestFrameJSONFixture(t *testing.T) {
	frame := Frame{
		Version:   1,
		RequestID: "req-1",
		Method:    MethodSessionHealth,
		Payload:   json.RawMessage(`{"session_id":"a"}`),
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Frame
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Method != MethodSessionHealth || string(decoded.Payload) != `{"session_id":"a"}` {
		t.Fatalf("unexpected round trip: %#v", decoded)
	}
}

func TestEventViewKeepsPayloadOpaque(t *testing.T) {
	view := EventView{
		SchemaVersion: 1,
		SessionID:     "sessiona",
		Seq:           1,
		EventID:       "sessiona-seq-1",
		Type:          "session.started",
		Visibility:    "internal",
		Payload:       json.RawMessage(`{"ok":true}`),
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded EventView
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.EventID != view.EventID || string(decoded.Payload) != `{"ok":true}` {
		t.Fatalf("event view round trip mismatch: %#v", decoded)
	}
}

func TestResumableObservationDORoundTrip(t *testing.T) {
	event := EventView{
		SchemaVersion: 1, SessionID: "sessiona", Seq: 7,
		EventID: "sessiona-seq-7", Type: "turn.started",
		Visibility: "conversation", Payload: json.RawMessage(`{"turn_id":"turn-7"}`),
	}
	watch := EventsResponse{
		Events: []EventView{event}, Watermark: 9, Cursor: 7, HasMore: true,
		ResyncRequired: false, TimedOut: false,
	}
	raw, err := json.Marshal(watch)
	if err != nil {
		t.Fatal(err)
	}
	var decoded EventsResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Cursor != 7 || !decoded.HasMore || len(decoded.Events) != 1 ||
		string(decoded.Events[0].Payload) != `{"turn_id":"turn-7"}` {
		t.Fatalf("watch round trip mismatch: %#v", decoded)
	}

	snapshot := SessionSnapshotResponse{
		SessionID: "sessiona", State: StateIdle, Watermark: 9,
		Events: []EventView{event}, HasMoreBefore: true,
	}
	raw, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decodedSnapshot SessionSnapshotResponse
	if err := json.Unmarshal(raw, &decodedSnapshot); err != nil {
		t.Fatal(err)
	}
	if decodedSnapshot.State != StateIdle || !decodedSnapshot.HasMoreBefore {
		t.Fatalf("snapshot round trip mismatch: %#v", decodedSnapshot)
	}
}
