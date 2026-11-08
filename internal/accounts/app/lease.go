package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"vibe-agent/internal/accounts/domain"
)

type LeaseManager struct {
	mu     sync.Mutex
	leases map[string]leaseRecord
	ttl    time.Duration
	now    func() time.Time
}

type leaseRecord struct {
	secret     []byte
	expiresAt  time.Time
	accountID  string
	capability domain.Capability
	purpose    string
}

func NewLeaseManager(ttl time.Duration) *LeaseManager {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &LeaseManager{leases: make(map[string]leaseRecord), ttl: ttl, now: time.Now}
}

func (m *LeaseManager) Issue(_ context.Context, account domain.Account, capability domain.Capability, purpose string) (IssuedLease, error) {
	binding, ok := account.Services[capability]
	if !ok {
		return IssuedLease{}, errors.New("account has no binding for capability")
	}
	secret, err := resolveCredentialRef(binding.CredentialRef)
	if err != nil {
		return IssuedLease{}, err
	}
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return IssuedLease{}, err
	}
	leaseID := base64.RawURLEncoding.EncodeToString(random)
	expiresAt := m.now().Add(m.ttl).UTC()
	m.mu.Lock()
	m.leases[leaseID] = leaseRecord{
		secret:     append([]byte(nil), secret...),
		expiresAt:  expiresAt,
		accountID:  account.ID,
		capability: capability,
		purpose:    purpose,
	}
	m.mu.Unlock()
	return IssuedLease{LeaseID: leaseID, ExpiresAt: expiresAt}, nil
}

func (m *LeaseManager) Redeem(_ context.Context, leaseID, invocationID string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.leases[leaseID]
	if !ok {
		return nil, errors.New("lease_not_found")
	}
	if !m.now().Before(lease.expiresAt) {
		delete(m.leases, leaseID)
		return nil, errors.New("lease_expired")
	}
	if invitationMismatch(lease.purpose, invocationID) {
		return nil, errors.New("lease_invocation_mismatch")
	}
	delete(m.leases, leaseID)
	return append([]byte(nil), lease.secret...), nil
}

func invitationMismatch(expected, actual string) bool {
	return expected != "" && actual != "" && expected != actual
}

func resolveCredentialRef(ref string) ([]byte, error) {
	scheme, locator, ok := strings.Cut(ref, ":")
	if !ok || locator == "" {
		return nil, errors.New("invalid credential reference")
	}
	switch scheme {
	case "env":
		value, exists := os.LookupEnv(locator)
		if !exists || value == "" {
			return nil, errors.New("missing_credential")
		}
		return []byte(value), nil
	default:
		return nil, errors.New("unsupported credential reference scheme")
	}
}
