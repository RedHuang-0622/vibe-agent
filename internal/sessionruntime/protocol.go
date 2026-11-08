package sessionruntime

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

const MaxFrameBytes = 8 << 20

func WriteFrame(writer io.Writer, frame any) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(data) > MaxFrameBytes {
		return fmt.Errorf("frame exceeds %d bytes", MaxFrameBytes)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := writer.Write(header); err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

func ReadFrame(reader io.Reader, target any) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header)
	if length == 0 || length > MaxFrameBytes {
		return fmt.Errorf("invalid frame length %d", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

type Handler interface {
	Handle(context.Context, string, json.RawMessage) (any, error)
}

type Server struct {
	listener net.Listener
	token    string
	handler  Handler
	wg       sync.WaitGroup
}

func NewServer(listener net.Listener, token string, handler Handler) (*Server, error) {
	if listener == nil || token == "" || handler == nil {
		return nil, errors.New("listener, token and handler are required")
	}
	return &Server{listener: listener, token: token, handler: handler}, nil
}

func (s *Server) Endpoint() string {
	return s.listener.Addr().String()
}

func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				break
			}
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConnection(ctx, connection)
		}()
	}
	s.wg.Wait()
	return nil
}

func (s *Server) Close() error {
	return s.listener.Close()
}

func (s *Server) handleConnection(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Minute))
	reader := bufio.NewReader(connection)
	for {
		var request sessionv1.Frame
		if err := ReadFrame(reader, &request); err != nil {
			return
		}
		response := sessionv1.FrameResponse{Version: sessionv1.Version, RequestID: request.RequestID}
		if request.Version != sessionv1.Version {
			response.Error = &sessionv1.Error{Code: "protocol_mismatch", Message: "unsupported frame version"}
		} else if request.RequestID == "" {
			response.Error = &sessionv1.Error{Code: "invalid_request", Message: "request_id is required"}
		} else if subtle.ConstantTimeCompare([]byte(request.Token), []byte(s.token)) != 1 {
			response.Error = &sessionv1.Error{Code: "permission_denied", Message: "invalid session token"}
		} else {
			payload, err := s.handler.Handle(ctx, request.Method, request.Payload)
			if err != nil {
				response.Error = publicError(err)
			} else {
				encoded, encodeErr := json.Marshal(payload)
				if encodeErr != nil {
					response.Error = &sessionv1.Error{Code: "internal_error", Message: "response encoding failed"}
				} else {
					response.OK = true
					response.Payload = encoded
				}
			}
		}
		if err := WriteFrame(connection, response); err != nil {
			return
		}
	}
}

type CodedError struct {
	Code      string
	Message   string
	Retryable bool
	Scope     string
	Provider  string
	RequestID string
	ResetAt   *time.Time
}

func (e *CodedError) Error() string {
	return e.Code + ": " + e.Message
}

func publicError(err error) *sessionv1.Error {
	var coded *CodedError
	if errors.As(err, &coded) {
		return &sessionv1.Error{
			Code: coded.Code, Message: coded.Message, Retryable: coded.Retryable,
			Scope: coded.Scope, Provider: coded.Provider,
			RequestID: coded.RequestID, ResetAt: coded.ResetAt,
		}
	}
	return &sessionv1.Error{Code: "internal_error", Message: "session command failed"}
}
