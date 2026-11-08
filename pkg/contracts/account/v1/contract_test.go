package accountv1

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestReaderSurfaceIsReadOnly(t *testing.T) {
	readerType := reflect.TypeOf((*Reader)(nil)).Elem()
	var methods []string
	for i := 0; i < readerType.NumMethod(); i++ {
		methods = append(methods, readerType.Method(i).Name)
	}
	sort.Strings(methods)
	want := []string{"Get", "ResolveForInvocation"}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("Reader methods = %v, want %v", methods, want)
	}
}

func TestAccountMethodClassification(t *testing.T) {
	for _, method := range AllMethods() {
		if !IsAccountMethod(method) {
			t.Errorf("%q should be an account method", method)
		}
	}
	if !IsSessiondReadMethod(MethodReadGet) || !IsSessiondReadMethod(MethodReadResolve) {
		t.Fatal("sessiond must be allowed to use both read methods")
	}
	for _, method := range []string{MethodAdminList, MethodAdminCreate, MethodAdminUpdate, MethodAdminSmoke} {
		if IsSessiondReadMethod(method) {
			t.Errorf("sessiond must not be allowed to call %q", method)
		}
	}
	if !IsWriteMethod(MethodAdminUpdate) || IsWriteMethod(MethodReadGet) {
		t.Fatal("write method classification is incorrect")
	}
}

func TestBindingViewDoesNotExposeCredentialReference(t *testing.T) {
	data, err := json.Marshal(BindingView{
		Capability: CapabilityChat, Model: "deepseek-flash",
		Thinking: &ThinkingConfig{Type: "enabled"}, ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "credential") {
		t.Fatalf("read model leaked credential data: %s", data)
	}
}

func TestDeepSeekBindingFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/deepseek-binding.json")
	if err != nil {
		t.Fatal(err)
	}
	var binding BindingInput
	if err := json.Unmarshal(data, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.APIKeyEnv != "DEEPSEEK_API_KEY" ||
		binding.Thinking == nil || binding.Thinking.Type != "enabled" ||
		binding.ReasoningEffort != "high" {
		t.Fatalf("binding=%#v", binding)
	}
	view := BindingView{
		Capability: CapabilityChat, Model: "deepseek-flash",
		Thinking: binding.Thinking, ReasoningEffort: binding.ReasoningEffort,
	}
	encoded, err := json.Marshal(AccountView{
		Ref:      AccountRef{AccountID: "deepseek", Revision: 1},
		Bindings: []BindingView{view},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "DEEPSEEK_API_KEY") ||
		strings.Contains(string(encoded), "credential") {
		t.Fatalf("account view leaked credential configuration: %s", encoded)
	}
}

func TestCapabilityValidation(t *testing.T) {
	for _, capability := range []Capability{
		CapabilityChat,
		CapabilityToolCall,
		CapabilityEmbedding,
		CapabilityRerank,
		CapabilitySearch,
	} {
		if !capability.Valid() {
			t.Errorf("%q should be valid", capability)
		}
	}
	if Capability("unknown").Valid() {
		t.Fatal("unknown capability should be invalid")
	}
}

func TestSmokeRequestFixtureRoundTrip(t *testing.T) {
	data, err := os.ReadFile("testdata/smoke-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var request SmokeRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Account.AccountID != "embedding-main" ||
		request.Capability != CapabilityEmbedding ||
		request.CaseID != "embedding-basic" ||
		request.TimeoutMS != 30000 ||
		request.Input == nil {
		t.Fatalf("request=%#v", request)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SmokeRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Input["texts"] == nil {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
}
