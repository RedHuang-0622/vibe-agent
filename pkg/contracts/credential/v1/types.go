package credentialv1

import (
	"context"
	"time"

	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

type LeaseRequest struct {
	Account    accountv1.AccountRef `json:"account"`
	Capability accountv1.Capability `json:"capability"`
	Purpose    string               `json:"purpose"`
	RequestID  string               `json:"request_id"`
}

type Lease struct {
	LeaseID   string    `json:"lease_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type RedeemRequest struct {
	LeaseID      string `json:"lease_id"`
	InvocationID string `json:"invocation_id"`
}

// RedeemResponse contains short-lived secret material. Callers must never log
// or persist the Secret field.
type RedeemResponse struct {
	Secret    []byte    `json:"secret"`
	Scheme    string    `json:"scheme"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Broker interface {
	Issue(ctx context.Context, req LeaseRequest) (Lease, error)
	Redeem(ctx context.Context, req RedeemRequest) (RedeemResponse, error)
}
