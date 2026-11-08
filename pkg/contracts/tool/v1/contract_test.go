package toolv1

import (
	"encoding/json"
	"testing"
)

func TestToolResultJSONFixture(t *testing.T) {
	result := Result{
		OK:      true,
		Payload: json.RawMessage(`{"value":7}`),
		Timing:  Timing{QueueMS: 1, ValidationMS: 1, ExecuteMS: 2, TotalMS: 4},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK || decoded.Timing.TotalMS != 4 {
		t.Fatalf("unexpected result: %#v", decoded)
	}
}
