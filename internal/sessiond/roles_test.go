package sessiond

import (
	"testing"

	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

func TestSessiondAllowsOnlyAccountReads(t *testing.T) {
	for _, method := range []string{accountv1.MethodReadGet, accountv1.MethodReadResolve} {
		if !CanInvokeAccountMethod(method) {
			t.Errorf("sessiond should allow %q", method)
		}
	}
	for _, method := range accountv1.AllMethods() {
		if accountv1.IsSessiondReadMethod(method) {
			continue
		}
		if CanInvokeAccountMethod(method) {
			t.Errorf("sessiond must reject %q", method)
		}
	}
}

func TestSessiondRejectsAllAccountWrites(t *testing.T) {
	for _, method := range accountv1.AllMethods() {
		if accountv1.IsWriteMethod(method) && CanInvokeAccountMethod(method) {
			t.Errorf("sessiond must reject write method %q", method)
		}
	}
}
