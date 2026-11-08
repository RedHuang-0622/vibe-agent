package knowledgev1

import (
	"encoding/json"
	"testing"
)

func TestHybridResultJSONFixture(t *testing.T) {
	rank := 1
	result := Result{
		Hits:      []Hit{{RecordID: "r1", ChunkID: "c1", Text: "bm25", LexicalRank: &rank}},
		Channels:  []Channel{ChannelBM25},
		Retrieval: Retrieval{IndexVersion: "v1", BM25Candidates: 1, Returned: 1},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Hits) != 1 || decoded.Retrieval.IndexVersion != "v1" {
		t.Fatalf("unexpected result: %#v", decoded)
	}
}
