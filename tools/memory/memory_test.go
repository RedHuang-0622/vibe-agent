package memory

import (
	"context"
	"encoding/json"
	"testing"

	"vibe-agent/internal/tooling"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
)

type fakeClient struct {
	lastQuery knowledgev1.Query
}

func (f *fakeClient) Write(context.Context, knowledgev1.WriteRequest) (knowledgev1.RecordRef, error) {
	return knowledgev1.RecordRef{RecordID: "r1", Version: 1, Status: "ready"}, nil
}

func (f *fakeClient) Search(_ context.Context, query knowledgev1.Query) (knowledgev1.Result, error) {
	f.lastQuery = query
	return knowledgev1.Result{Hits: []knowledgev1.Hit{{RecordID: "r1", ChunkID: "c1", Text: query.Text}}}, nil
}

func (f *fakeClient) Update(context.Context, knowledgev1.UpdateRequest) (knowledgev1.RecordRef, error) {
	return knowledgev1.RecordRef{RecordID: "r1", Version: 2, Status: "ready"}, nil
}

func TestMemorySearchDirectRequest(t *testing.T) {
	client := &fakeClient{}
	tool := NewSearch(client)
	adapted, err := (SearchAdapter{}).Adapt("顺序规则")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := tool.Invoke(context.Background(), tooling.Call{Arguments: adapted})
	if err != nil {
		t.Fatal(err)
	}
	if client.lastQuery.Text != "顺序规则" || client.lastQuery.Scope != "workspace" {
		t.Fatalf("query=%#v", client.lastQuery)
	}
	raw, _ := json.Marshal(payload)
	if len(raw) == 0 {
		t.Fatal("empty payload")
	}
}

func TestMemoryWriteDirectRequest(t *testing.T) {
	adapted, err := (WriteAdapter{}).Adapt(`scope=workspace text="k1=1.2, b=0.75"`)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(adapted, &value); err != nil {
		t.Fatal(err)
	}
	if value["scope"] != "workspace" || value["text"] != "k1=1.2, b=0.75" {
		t.Fatalf("value=%v", value)
	}
}
