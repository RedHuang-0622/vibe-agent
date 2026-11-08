package hub

import (
	"testing"

	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

func TestHubAllowsAccountReadAndWrite(t *testing.T) {
	for _, method := range accountv1.AllMethods() {
		if !CanInvokeAccountMethod(method) {
			t.Errorf("hub should allow %q", method)
		}
	}
}

func TestHubRejectsUnknownAccountMethod(t *testing.T) {
	if CanInvokeAccountMethod("account.admin.unknown") {
		t.Fatal("unknown method must be rejected")
	}
}
