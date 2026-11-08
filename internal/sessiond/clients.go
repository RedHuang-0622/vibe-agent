package sessiond

import (
	"context"
	"errors"

	"vibe-agent/internal/llm"
	"vibe-agent/internal/sessionruntime"
	"vibe-agent/internal/tooling"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
	credentialv1 "vibe-agent/pkg/contracts/credential/v1"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

type hubAccountClient struct {
	hub *sessionruntime.HubClient
}

func (c *hubAccountClient) Get(ctx context.Context, ref accountv1.AccountRef) (accountv1.AccountView, error) {
	var view accountv1.AccountView
	err := c.hub.Invoke(ctx, accountv1.MethodReadGet, ref, &view)
	return view, mapHubAccountError(err)
}

func (c *hubAccountClient) ResolveForInvocation(ctx context.Context, request accountv1.ResolveRequest) (accountv1.ResolvedInvocation, error) {
	var invocation accountv1.ResolvedInvocation
	err := c.hub.Invoke(ctx, accountv1.MethodReadResolve, request, &invocation)
	return invocation, mapHubAccountError(err)
}

func (c *hubAccountClient) Redeem(ctx context.Context, leaseID, invocationID string) ([]byte, error) {
	var response credentialv1.RedeemResponse
	request := credentialv1.RedeemRequest{LeaseID: leaseID, InvocationID: invocationID}
	if err := c.hub.Invoke(ctx, sessionv1.MethodCredentialRedeem, request, &response); err != nil {
		return nil, err
	}
	return response.Secret, nil
}

func mapHubAccountError(err error) error {
	if err == nil {
		return nil
	}
	if coded, ok := err.(*sessionruntime.CodedError); ok {
		return &accountv1.ContractError{
			Code: accountv1.ErrorCode(coded.Code), Message: coded.Message, Retryable: coded.Retryable,
		}
	}
	return err
}

type hubKnowledgeClient struct {
	hub *sessionruntime.HubClient
}

func (c *hubKnowledgeClient) Write(ctx context.Context, request knowledgev1.WriteRequest) (knowledgev1.RecordRef, error) {
	var ref knowledgev1.RecordRef
	err := c.hub.Invoke(ctx, sessionv1.MethodKnowledgeWrite, request, &ref)
	return ref, mapKnowledgeClientError(err)
}

func (c *hubKnowledgeClient) Search(ctx context.Context, request knowledgev1.Query) (knowledgev1.Result, error) {
	var result knowledgev1.Result
	err := c.hub.Invoke(ctx, sessionv1.MethodKnowledgeSearch, request, &result)
	return result, mapKnowledgeClientError(err)
}

func (c *hubKnowledgeClient) Update(ctx context.Context, request knowledgev1.UpdateRequest) (knowledgev1.RecordRef, error) {
	var ref knowledgev1.RecordRef
	err := c.hub.Invoke(ctx, sessionv1.MethodKnowledgeUpdate, request, &ref)
	return ref, mapKnowledgeClientError(err)
}

func mapKnowledgeClientError(err error) error {
	if err == nil {
		return nil
	}
	if coded, ok := err.(*sessionruntime.CodedError); ok {
		toolErr := tooling.NewError(coded.Code, coded.Message, coded.Retryable)
		toolErr.Scope = coded.Scope
		toolErr.Provider = coded.Provider
		toolErr.RequestID = coded.RequestID
		toolErr.ResetAt = coded.ResetAt
		return toolErr
	}
	return tooling.NewError(tooling.CodeDependency, "memory service is unavailable", true)
}

type remoteResolver struct {
	gateway    *llm.Gateway
	capability accountv1.Capability
}

func (r remoteResolver) Resolve(ctx context.Context) (tooling.RemoteConfig, error) {
	config, err := r.gateway.ResolveRemote(ctx, r.capability, string(r.capability))
	if err != nil {
		return tooling.RemoteConfig{}, mapLLMToolError(err)
	}
	return tooling.RemoteConfig{
		BaseURL: config.BaseURL, Path: config.Path, Model: config.Model,
		APIKey: config.APIKey, Provider: config.Provider,
		TimeoutMS: config.TimeoutMS,
	}, nil
}

func mapLLMToolError(err error) error {
	var modelErr *llm.Error
	if errors.As(err, &modelErr) {
		toolErr := tooling.NewError(modelErr.Code, modelErr.Message, modelErr.Retryable)
		toolErr.Scope = modelErr.Scope
		toolErr.Provider = modelErr.Provider
		toolErr.RequestID = modelErr.RequestID
		toolErr.ResetAt = modelErr.ResetAt
		return toolErr
	}
	return tooling.NewError(tooling.CodeDependency, "provider is unavailable", true)
}
