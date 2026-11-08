package sessionruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

type echoHandler struct{}

func (echoHandler) Handle(_ context.Context, method string, payload json.RawMessage) (any, error) {
	if method != sessionv1.MethodSessionHealth {
		return nil, &CodedError{Code: "unknown_method", Message: "unknown"}
	}
	return map[string]string{"payload": string(payload)}, nil
}

func TestFramedClientServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(listener, "token", echoHandler{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	client := NewClient(server.Endpoint(), "token")
	var response map[string]string
	if err := client.Do(context.Background(), sessionv1.MethodSessionHealth, map[string]string{"x": "y"}, &response); err != nil {
		t.Fatal(err)
	}
	if response["payload"] != `{"x":"y"}` {
		t.Fatalf("response=%v", response)
	}
}

func TestFrameLimit(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, map[string]string{"ok": "yes"}); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := ReadFrame(&buffer, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["ok"] != "yes" {
		t.Fatalf("decoded=%v", decoded)
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	path := t.TempDir() + "/sessiond.json"
	want := Metadata{
		SessionID: "a", PID: 1, Generation: 2,
		Endpoint: "127.0.0.1:1234", Token: "t", Protocol: "v1", StartedAt: time.Now(),
	}
	if err := WriteMetadata(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMetadata(path)
	if err != nil || got.SessionID != want.SessionID || got.Generation != want.Generation {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestClientRejectsMismatchedResponseID(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		var request sessionv1.Frame
		if ReadFrame(connection, &request) != nil {
			return
		}
		_ = WriteFrame(connection, sessionv1.FrameResponse{
			Version: sessionv1.Version, RequestID: "wrong", OK: true,
		})
	}()
	client := NewClient(listener.Addr().String(), "token")
	var target map[string]string
	if err := client.Do(context.Background(), sessionv1.MethodSessionHealth, struct{}{}, &target); err == nil {
		t.Fatal("mismatched response request id must fail")
	}
}

func TestServerRequiresRequestID(t *testing.T) {
	var input bytes.Buffer
	var output bytes.Buffer
	if err := WriteFrame(&input, sessionv1.Frame{
		Version: sessionv1.Version, Method: sessionv1.MethodSessionHealth, Token: "token",
	}); err != nil {
		t.Fatal(err)
	}
	connection := &memoryConn{reader: bytes.NewReader(input.Bytes()), writer: &output}
	server := &Server{token: "token", handler: echoHandler{}}
	server.handleConnection(context.Background(), connection)
	var response sessionv1.FrameResponse
	var reader bytes.Buffer
	reader.Write(output.Bytes())
	if err := ReadFrame(&reader, &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != "invalid_request" {
		t.Fatalf("response=%#v", response)
	}
}

type memoryConn struct {
	reader *bytes.Reader
	writer *bytes.Buffer
}

func (c *memoryConn) Read(value []byte) (int, error)  { return c.reader.Read(value) }
func (c *memoryConn) Write(value []byte) (int, error) { return c.writer.Write(value) }
func (c *memoryConn) Close() error                    { return nil }
func (c *memoryConn) LocalAddr() net.Addr             { return fakeAddr("local") }
func (c *memoryConn) RemoteAddr() net.Addr            { return fakeAddr("remote") }
func (c *memoryConn) SetDeadline(time.Time) error     { return nil }
func (c *memoryConn) SetReadDeadline(time.Time) error { return nil }
func (c *memoryConn) SetWriteDeadline(time.Time) error {
	return nil
}

type fakeAddr string

func (a fakeAddr) Network() string { return string(a) }
func (a fakeAddr) String() string  { return string(a) }

func FuzzReadFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		var target map[string]any
		_ = ReadFrame(bytes.NewReader(data), &target)
	})
}
