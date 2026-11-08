package accountv1

import "fmt"

type ErrorCode string

const (
	ErrorInvalidRequest       ErrorCode = "invalid_request"
	ErrorPermissionDenied     ErrorCode = "permission_denied"
	ErrorNotFound             ErrorCode = "not_found"
	ErrorConflict             ErrorCode = "conflict"
	ErrorMissingCredential    ErrorCode = "missing_credential"
	ErrorAuthenticationFailed ErrorCode = "authentication_failed"
	ErrorRateLimited          ErrorCode = "rate_limited"
	ErrorQuotaExhausted       ErrorCode = "quota_exhausted"
	ErrorDependency           ErrorCode = "dependency_unavailable"
)

type ContractError struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

func (e *ContractError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
