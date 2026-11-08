package hub

import (
	"context"
	"encoding/json"

	"vibe-agent/internal/sessionruntime"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

func (s *Server) invokeAccount(ctx context.Context, method string, payload json.RawMessage) (any, error) {
	switch method {
	case accountv1.MethodReadGet:
		var request accountv1.AccountRef
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.Get(ctx, request)
	case accountv1.MethodReadResolve:
		var request accountv1.ResolveRequest
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.ResolveForInvocation(ctx, request)
	case accountv1.MethodAdminList:
		var request accountv1.ListFilter
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.List(ctx, request)
	case accountv1.MethodAdminCreate:
		var request accountv1.CreateAccount
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.Create(ctx, request)
	case accountv1.MethodAdminUpdate:
		var request accountv1.UpdateAccount
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.Update(ctx, request)
	case accountv1.MethodAdminSetDefault:
		var request accountv1.SetDefault
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.SetDefault(ctx, request)
	case accountv1.MethodAdminSetEnabled:
		var request accountv1.SetEnabled
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.SetEnabled(ctx, request)
	case accountv1.MethodAdminRotateRef:
		var request accountv1.RotateCredentialRef
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.RotateCredentialRef(ctx, request)
	case accountv1.MethodAdminArchive:
		var request accountv1.AccountRef
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		if err := s.accounts.Archive(ctx, request); err != nil {
			return nil, err
		}
		return map[string]bool{"archived": true}, nil
	case accountv1.MethodAdminValidate:
		var request accountv1.ValidateAccount
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.Validate(ctx, request)
	case accountv1.MethodAdminSmoke:
		var request accountv1.SmokeRequest
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
		return s.accounts.Smoke(ctx, request)
	default:
		return nil, &sessionruntime.CodedError{Code: "not_found", Message: "unknown account method"}
	}
}
