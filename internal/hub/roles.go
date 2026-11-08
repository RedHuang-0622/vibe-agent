package hub

import accountv1 "vibe-agent/pkg/contracts/account/v1"

func CanInvokeAccountMethod(method string) bool {
	return accountv1.IsAccountMethod(method)
}
