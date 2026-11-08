package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	accountapp "vibe-agent/internal/accounts/app"
	"vibe-agent/internal/knowledge"
	"vibe-agent/internal/sessionruntime"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
	credentialv1 "vibe-agent/pkg/contracts/credential/v1"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type SessionManager interface {
	Create(context.Context, sessionv1.CreateSessionRequest) (sessionv1.SessionView, error)
	List(context.Context) ([]sessionv1.SessionView, error)
	Inspect(context.Context, string) (sessionv1.SessionView, error)
	Route(context.Context, string, string, any, any) error
	Stop(context.Context, string) error
}

type KnowledgeService interface {
	Write(context.Context, knowledgev1.WriteRequest) (knowledgev1.RecordRef, error)
	Search(context.Context, knowledgev1.Query) (knowledgev1.Result, error)
	Update(context.Context, knowledgev1.UpdateRequest) (knowledgev1.RecordRef, error)
}

type LeaseStore interface {
	Redeem(context.Context, string, string) ([]byte, error)
}

type Server struct {
	listener   net.Listener
	adminToken string
	agentToken string
	sessions   SessionManager
	accounts   accountv1.Admin
	knowledge  KnowledgeService
	leases     LeaseStore
	httpServer *http.Server
}

type StatusResponse struct {
	Endpoint string `json:"endpoint"`
	Version  string `json:"version"`
}

type ToolListRequest struct {
	SessionID string `json:"session_id"`
}

func NewServer(listener net.Listener, adminToken, agentToken string, sessions SessionManager, accounts accountv1.Admin, knowledge KnowledgeService, leases LeaseStore) (*Server, error) {
	if listener == nil || adminToken == "" || agentToken == "" || sessions == nil || accounts == nil {
		return nil, errors.New("hub server configuration is incomplete")
	}
	server := &Server{
		listener: listener, adminToken: adminToken, agentToken: agentToken,
		sessions: sessions, accounts: accounts, knowledge: knowledge, leases: leases,
	}
	server.httpServer = &http.Server{
		Handler:           http.HandlerFunc(server.handleHTTP),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server, nil
}

func (s *Server) Endpoint() string {
	return s.listener.Addr().String()
}

func (s *Server) Serve() error {
	err := s.httpServer.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Close(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	if request.Method != http.MethodPost || request.URL.Path != "/v1/invoke" {
		writeHTTPEnvelope(writer, sessionv1.HTTPResponse{Error: &sessionv1.Error{Code: "not_found", Message: "endpoint not found"}})
		return
	}
	role := s.role(request.Header.Get("X-Vibe-Token"))
	if role == "" {
		writer.WriteHeader(http.StatusUnauthorized)
		writeHTTPEnvelope(writer, sessionv1.HTTPResponse{Error: &sessionv1.Error{Code: "authentication_failed", Message: "invalid hub token"}})
		return
	}
	var envelope sessionv1.HTTPRequest
	if err := json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&envelope); err != nil {
		writeHTTPEnvelope(writer, sessionv1.HTTPResponse{Error: &sessionv1.Error{Code: "invalid_request", Message: "invalid request body"}})
		return
	}
	ctx := accountapp.WithAuditIdentity(
		request.Context(), "hub:"+role, request.Header.Get("X-Request-ID"),
	)
	payload, err := s.invoke(ctx, role, envelope.Method, envelope.Payload)
	if err != nil {
		writeHTTPEnvelope(writer, sessionv1.HTTPResponse{Error: publicHubError(err)})
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		writeHTTPEnvelope(writer, sessionv1.HTTPResponse{Error: &sessionv1.Error{Code: "internal_error", Message: "response encoding failed"}})
		return
	}
	writeHTTPEnvelope(writer, sessionv1.HTTPResponse{OK: true, Payload: encoded})
}

func (s *Server) role(token string) string {
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.adminToken)) == 1 {
		return "admin"
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.agentToken)) == 1 {
		return "agent"
	}
	return ""
}

func (s *Server) invoke(ctx context.Context, role, method string, payload json.RawMessage) (any, error) {
	if strings.HasPrefix(method, "account.") {
		if role != "admin" && !accountv1.IsSessiondReadMethod(method) {
			return nil, &accountv1.ContractError{Code: accountv1.ErrorPermissionDenied, Message: "account method denied"}
		}
		return s.invokeAccount(ctx, method, payload)
	}
	if role != "admin" {
		switch method {
		case sessionv1.MethodCredentialRedeem, sessionv1.MethodKnowledgeWrite,
			sessionv1.MethodKnowledgeSearch, sessionv1.MethodKnowledgeUpdate:
		default:
			return nil, &sessionruntime.CodedError{Code: "permission_denied", Message: "hub method denied for sessiond token"}
		}
	}
	switch method {
	case sessionv1.MethodHubStatus:
		return StatusResponse{Endpoint: s.Endpoint(), Version: sessionv1.ProtocolVersion}, nil
	case sessionv1.MethodHubShutdown:
		go func() {
			time.Sleep(100 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.httpServer.Shutdown(ctx)
		}()
		return map[string]bool{"stopping": true}, nil
	case sessionv1.MethodHubCreateSession:
		var req sessionv1.CreateSessionRequest
		if err := decode(payload, &req); err != nil {
			return nil, err
		}
		view, err := s.sessions.Create(ctx, req)
		return sessionv1.CreateSessionResponse{Session: view}, err
	case sessionv1.MethodHubListSessions:
		views, err := s.sessions.List(ctx)
		return map[string]any{"sessions": views}, err
	case sessionv1.MethodHubInspect:
		var req sessionv1.SessionRef
		if err := decode(payload, &req); err != nil {
			return nil, err
		}
		view, err := s.sessions.Inspect(ctx, req.SessionID)
		return sessionv1.CreateSessionResponse{Session: view}, err
	case sessionv1.MethodHubStopSession:
		var req sessionv1.SessionRef
		if err := decode(payload, &req); err != nil {
			return nil, err
		}
		if err := s.sessions.Stop(ctx, req.SessionID); err != nil {
			return nil, err
		}
		return map[string]bool{"stopped": true}, nil
	case sessionv1.MethodHubToolList:
		var req ToolListRequest
		if err := decode(payload, &req); err != nil {
			return nil, err
		}
		var target toolv1.ListResponse
		err := s.sessions.Route(ctx, req.SessionID, sessionv1.MethodHubToolList, struct{}{}, &target)
		return target, err
	case sessionv1.MethodKnowledgeWrite:
		var req knowledgev1.WriteRequest
		if err := decode(payload, &req); err != nil {
			return nil, err
		}
		return s.knowledge.Write(ctx, req)
	case sessionv1.MethodKnowledgeSearch:
		var req knowledgev1.Query
		if err := decode(payload, &req); err != nil {
			return nil, err
		}
		return s.knowledge.Search(ctx, req)
	case sessionv1.MethodKnowledgeUpdate:
		var req knowledgev1.UpdateRequest
		if err := decode(payload, &req); err != nil {
			return nil, err
		}
		return s.knowledge.Update(ctx, req)
	case sessionv1.MethodCredentialRedeem:
		var req credentialv1.RedeemRequest
		if err := decode(payload, &req); err != nil {
			return nil, err
		}
		secret, err := s.leases.Redeem(ctx, req.LeaseID, req.InvocationID)
		if err != nil {
			return nil, &sessionruntime.CodedError{Code: "missing_credential", Message: "credential lease is unavailable"}
		}
		return credentialv1.RedeemResponse{Secret: secret}, nil
	case sessionv1.MethodSessionSubmit, sessionv1.MethodSessionDirect,
		sessionv1.MethodSessionCancel, sessionv1.MethodSessionCompress,
		sessionv1.MethodSessionEvents, sessionv1.MethodSessionWatch,
		sessionv1.MethodSessionSnapshot, sessionv1.MethodSessionHistory,
		sessionv1.MethodSessionTrace,
		sessionv1.MethodSessionStats, sessionv1.MethodSessionShutdown:
		return s.routeSession(ctx, method, payload)
	default:
		return nil, &sessionruntime.CodedError{Code: "not_found", Message: "unknown hub method"}
	}
}

func (s *Server) routeSession(ctx context.Context, method string, payload json.RawMessage) (any, error) {
	var ref sessionv1.SessionRef
	if err := json.Unmarshal(payload, &ref); err != nil || ref.SessionID == "" {
		return nil, &sessionruntime.CodedError{Code: "invalid_request", Message: "session_id is required"}
	}
	var target json.RawMessage
	if err := s.sessions.Route(ctx, ref.SessionID, method, payload, &target); err != nil {
		return nil, err
	}
	return target, nil
}

func decode(payload json.RawMessage, target any) error {
	if err := json.Unmarshal(payload, target); err != nil {
		return &sessionruntime.CodedError{Code: "invalid_request", Message: "invalid method payload"}
	}
	return nil
}

func writeHTTPEnvelope(writer http.ResponseWriter, envelope sessionv1.HTTPResponse) {
	_ = json.NewEncoder(writer).Encode(envelope)
}

func publicHubError(err error) *sessionv1.Error {
	var accountErr *accountv1.ContractError
	if errors.As(err, &accountErr) {
		return &sessionv1.Error{Code: string(accountErr.Code), Message: accountErr.Message, Retryable: accountErr.Retryable}
	}
	var coded *sessionruntime.CodedError
	if errors.As(err, &coded) {
		return &sessionv1.Error{
			Code: coded.Code, Message: coded.Message, Retryable: coded.Retryable,
			Scope: coded.Scope, Provider: coded.Provider,
			RequestID: coded.RequestID, ResetAt: coded.ResetAt,
		}
	}
	var serviceErr *knowledge.ServiceError
	if errors.As(err, &serviceErr) {
		return &sessionv1.Error{
			Code: serviceErr.Code, Message: serviceErr.Message, Retryable: serviceErr.Retryable,
			Scope: serviceErr.Scope, Provider: serviceErr.Provider,
			RequestID: serviceErr.RequestID, ResetAt: serviceErr.ResetAt,
		}
	}
	return &sessionv1.Error{Code: "internal_error", Message: "hub request failed"}
}
