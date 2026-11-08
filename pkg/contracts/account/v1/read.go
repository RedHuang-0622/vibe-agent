package accountv1

import "context"

// Reader is the only account surface available to sessiond and read-only
// consumers. It deliberately omits list and every mutating operation.
type Reader interface {
	Get(ctx context.Context, ref AccountRef) (AccountView, error)
	ResolveForInvocation(ctx context.Context, req ResolveRequest) (ResolvedInvocation, error)
}
