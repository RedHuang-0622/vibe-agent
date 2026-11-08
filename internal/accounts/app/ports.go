package app

import (
	"context"
	"time"

	"vibe-agent/internal/accounts/domain"
)

type Repository interface {
	List(ctx context.Context) ([]domain.Account, error)
	Get(ctx context.Context, accountID string) (domain.Account, error)
	Save(ctx context.Context, account domain.Account, expectedRevision uint64) error
}

type LeaseIssuer interface {
	Issue(ctx context.Context, account domain.Account, capability domain.Capability, purpose string) (IssuedLease, error)
}

type SmokeRunner interface {
	Run(ctx context.Context, account domain.Account, capability domain.Capability, caseID string, input map[string]any, timeoutMS int64) error
}

type IssuedLease struct {
	LeaseID   string
	ExpiresAt time.Time
}

type AuditEntry struct {
	ActorID          string    `json:"actor_id"`
	Action           string    `json:"action"`
	AccountID        string    `json:"account_id"`
	ExpectedRevision uint64    `json:"expected_revision"`
	ResultRevision   uint64    `json:"result_revision"`
	ChangedFields    []string  `json:"changed_fields,omitempty"`
	RequestID        string    `json:"request_id,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
}

type AuditWriter interface {
	Write(ctx context.Context, entry AuditEntry) error
}
