package knowledge

import (
	"context"
	"math"
	"testing"
)

func TestDefaultTokenizerCJK(t *testing.T) {
	got := DefaultTokenizer{}.Tokenize("Go 工具调用")
	want := map[string]bool{"go": true, "工": true, "具": true, "调": true, "用": true, "工具": true, "具调": true, "调用": true}
	for _, token := range got {
		delete(want, token)
	}
	if len(want) != 0 {
		t.Fatalf("tokenizer missed %v from %v", want, got)
	}
}

func TestBM25Formula(t *testing.T) {
	index := NewBM25Index(DefaultTokenizer{}, 1.2, 0.75)
	index.Replace(context.Background(), []LexicalDocument{
		{RecordID: "r1", ChunkID: "c1", Scope: "workspace", Text: "go agent"},
		{RecordID: "r2", ChunkID: "c2", Scope: "workspace", Text: "go go runtime"},
	})
	hits := index.Search(context.Background(), "go", "workspace", 10)
	if len(hits) != 2 {
		t.Fatalf("hits=%v", hits)
	}
	// N=2, df=2, avgdl=2.5, idf=ln(1+0.5/2.5).
	idf := math.Log(1 + (2-2+0.5)/(2+0.5))
	wantC2 := idf * (2 * 2.2) / (2 + 1.2*(0.25+0.75*(3.0/2.5)))
	if hits[0].ChunkID != "c2" || math.Abs(hits[0].Score-wantC2) > 1e-9 {
		t.Fatalf("hit=%#v want score %.12f", hits[0], wantC2)
	}
}

func TestBM25UpdateDeleteAndStableTieBreak(t *testing.T) {
	index := NewBM25Index(DefaultTokenizer{}, 1.2, 0.75)
	index.Replace(context.Background(), []LexicalDocument{
		{ChunkID: "b", RecordID: "rb", Text: "same"},
		{ChunkID: "a", RecordID: "ra", Text: "same"},
	})
	hits := index.Search(context.Background(), "same", "", 10)
	if hits[0].ChunkID != "a" || hits[1].ChunkID != "b" {
		t.Fatalf("unstable tie break: %v", hits)
	}
	index.Delete(context.Background(), []string{"a"})
	hits = index.Search(context.Background(), "same", "", 10)
	if len(hits) != 1 || hits[0].ChunkID != "b" {
		t.Fatalf("delete failed: %v", hits)
	}
	index.Upsert(context.Background(), []LexicalDocument{{ChunkID: "b", RecordID: "rb", Text: "changed"}})
	if hits := index.Search(context.Background(), "same", "", 10); len(hits) != 0 {
		t.Fatalf("update failed: %v", hits)
	}
}

func FuzzDefaultTokenizer(f *testing.F) {
	f.Add("Go 工具调用")
	f.Add("BM25 mixed 中文")
	f.Fuzz(func(t *testing.T, text string) {
		tokens := (DefaultTokenizer{}).Tokenize(text)
		for _, token := range tokens {
			if token == "" {
				t.Fatal("tokenizer returned an empty token")
			}
		}
	})
}
