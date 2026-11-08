package operator

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"vibe-agent/internal/sessionruntime"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

func RunHubCLI(ctx context.Context, client *Client, args []string, output, errorOutput io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(output, hubHelp)
		return nil
	}
	switch args[0] {
	case "status":
		status, err := client.Status(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "%s (%s)\n", status.Endpoint, status.Version)
		return nil
	case "daemon-shutdown":
		return client.Invoke(ctx, sessionv1.MethodHubShutdown, struct{}{}, nil)
	case "session":
		return runSessionCLI(ctx, client, args[1:], output)
	case "run", "chat":
		if len(args) < 3 {
			return fmt.Errorf("usage: hub run|chat <session_id> <text>")
		}
		return runChatCLI(ctx, client, args[1], strings.Join(args[2:], " "), output)
	case "tool":
		return runToolCLI(ctx, client, args[1:], output)
	case "compress":
		return runCompressCLI(ctx, client, args[1:], output, errorOutput)
	case "trace":
		return runTraceCLI(ctx, client, args[1:], output, errorOutput)
	case "stats":
		return runStatsCLI(ctx, client, args[1:], output, errorOutput)
	case "events":
		return runEventsCLI(ctx, client, args[1:], output, errorOutput)
	case "snapshot":
		return runSnapshotCLI(ctx, client, args[1:], output, errorOutput)
	case "history":
		return runHistoryCLI(ctx, client, args[1:], output, errorOutput)
	case "llm":
		return runAccountCLI(ctx, client, args[1:], output)
	default:
		return fmt.Errorf("unknown hub command %q", args[0])
	}
}

func runSessionCLI(ctx context.Context, client *Client, args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: hub session create|list|inspect|stop")
	}
	switch args[0] {
	case "create":
		name := ""
		if len(args) > 1 {
			name = strings.Join(args[1:], " ")
		}
		view, err := client.CreateSession(ctx, name)
		if err != nil {
			return err
		}
		fmt.Fprintln(output, view.SessionID)
		return nil
	case "list":
		sessions, err := client.ListSessions(ctx)
		if err != nil {
			return err
		}
		for _, session := range sessions {
			fmt.Fprintf(output, "%s\t%s\t%s\tseq=%d\tpid=%d\n",
				session.SessionID, session.Name, session.State, session.LastSeq, session.PID)
		}
		return nil
	case "inspect":
		if len(args) < 2 {
			return fmt.Errorf("usage: hub session inspect <session_id>")
		}
		view, err := client.InspectSession(ctx, args[1])
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(view)
		fmt.Fprintln(output, string(encoded))
		return nil
	case "stop":
		if len(args) < 2 {
			return fmt.Errorf("usage: hub session stop <session_id>")
		}
		return client.StopSession(ctx, args[1])
	default:
		return fmt.Errorf("unknown session command %q", args[0])
	}
}

func runChatCLI(ctx context.Context, client *Client, sessionID, text string, output io.Writer) error {
	before, err := client.InspectSession(ctx, sessionID)
	if err != nil {
		return err
	}
	submitted, err := client.Submit(ctx, sessionID, text, "queue")
	if err != nil {
		return err
	}
	return WaitForMessageTurn(ctx, client, sessionID, submitted.MessageID, before.LastSeq, output)
}

func runToolCLI(ctx context.Context, client *Client, args []string, output io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: hub tool list <session_id> | call <session_id> /<tool> <request>")
	}
	switch args[0] {
	case "list":
		if len(args) < 2 {
			return fmt.Errorf("usage: hub tool list <session_id>")
		}
		tools, err := client.Tools(ctx, args[1])
		if err != nil {
			return err
		}
		for _, tool := range tools.Tools {
			fmt.Fprintf(output, "%s\t%s\t%s\n", tool.Name, tool.Kind, tool.Description)
		}
		return nil
	case "call":
		if len(args) < 4 {
			return fmt.Errorf("usage: hub tool call <session_id> /<tool> <request>")
		}
		rawRequest := strings.Join(args[3:], " ")
		if strings.HasPrefix(rawRequest, "@") {
			data, err := os.ReadFile(strings.TrimSpace(strings.TrimPrefix(rawRequest, "@")))
			if err != nil {
				return err
			}
			rawRequest = strings.TrimSpace(string(data))
		}
		request := args[2] + " " + rawRequest
		result, err := client.DirectTool(ctx, args[1], request)
		if err != nil {
			return err
		}
		if result.Error != nil {
			return fmt.Errorf("%s: %s retryable=%v reset_at=%v",
				result.Error.Code, result.Error.Message, result.Error.Retryable, result.Error.ResetAt)
		}
		fmt.Fprintln(output, string(result.Result))
		return nil
	default:
		return fmt.Errorf("unknown tool command")
	}
}

func runCompressCLI(ctx context.Context, client *Client, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("compress", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	auto := flags.Bool("auto", false, "autonomous trigger")
	through := flags.Int64("through-seq", 0, "safe target sequence")
	if len(args) < 1 {
		return fmt.Errorf("usage: hub compress <session_id> [--auto|--through-seq N]")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	trigger := "external"
	if *auto {
		trigger = "autonomous"
	}
	result, err := client.Compress(ctx, sessionv1.CompressionRequest{
		SessionID: args[0], Trigger: trigger, ThroughSeq: *through,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "committed=%v red_line_seq=%d error=%s\n", result.Committed, result.RedLineSeq, result.ErrorCode)
	return nil
}

func runTraceCLI(ctx context.Context, client *Client, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("trace", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	callID := flags.String("call-id", "", "tool call id")
	if len(args) < 1 {
		return fmt.Errorf("usage: hub trace <session_id> [--call-id ID]")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	result, err := client.Trace(ctx, sessionv1.TraceRequest{SessionID: args[0], CallID: *callID})
	if err != nil {
		return err
	}
	for _, node := range result.Nodes {
		fmt.Fprintf(output, "%d\t%s\t%s\t%s\n", node.Seq, node.Type, node.CallID, node.Status)
	}
	return nil
}

func runStatsCLI(ctx context.Context, client *Client, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("stats", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	toolName := flags.String("tool", "", "filter tool")
	mode := flags.String("mode", "", "model or direct")
	if len(args) < 1 {
		return fmt.Errorf("usage: hub stats <session_id>")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	result, err := client.Stats(ctx, sessionv1.StatsRequest{SessionID: args[0], ToolName: *toolName, Mode: *mode})
	if err != nil {
		return err
	}
	for _, stat := range result.ByTool {
		fmt.Fprintf(output, "%s\t%s\t%s\tcalls=%d\tfailed=%d\tp50=%.1f\tp95=%.1f\n",
			stat.ToolName, stat.ToolKind, stat.InvocationMode, stat.Calls, stat.Failed,
			stat.Total.P50MS, stat.Total.P95MS)
	}
	return nil
}

func runEventsCLI(ctx context.Context, client *Client, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("events", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	after := flags.Int64("after", 0, "return events after seq")
	limit := flags.Int("limit", 100, "maximum page size")
	waitMS := flags.Int("wait-ms", 0, "long-poll wait in milliseconds")
	if len(args) < 1 {
		return fmt.Errorf("usage: hub events <session_id> [--after N] [--limit N] [--wait-ms N]")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	result, err := client.Watch(ctx, sessionv1.WatchRequest{
		SessionID: args[0], AfterSeq: *after, Limit: *limit, WaitMS: *waitMS,
	})
	if err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(output, string(encoded))
	return nil
}

func runSnapshotCLI(ctx context.Context, client *Client, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	tailLimit := flags.Int("tail-limit", 100, "maximum latest events")
	if len(args) < 1 {
		return fmt.Errorf("usage: hub snapshot <session_id> [--tail-limit N]")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	result, err := client.Snapshot(ctx, args[0], *tailLimit)
	if err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(output, string(encoded))
	return nil
}

func runHistoryCLI(ctx context.Context, client *Client, args []string, output, errorOutput io.Writer) error {
	flags := flag.NewFlagSet("history", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	before := flags.Int64("before-seq", 0, "return events strictly before this seq")
	limit := flags.Int("limit", 100, "maximum page size")
	if len(args) < 1 {
		return fmt.Errorf("usage: hub history <session_id> [--before-seq N] [--limit N]")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	result, err := client.History(ctx, sessionv1.SessionHistoryRequest{
		SessionID: args[0], BeforeSeq: *before, Limit: *limit,
	})
	if err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(output, string(encoded))
	return nil
}

func runAccountCLI(ctx context.Context, client *Client, args []string, output io.Writer) error {
	if len(args) < 2 || args[0] != "account" {
		return fmt.Errorf("usage: hub llm account list|show|add|use|remove|validate|smoke")
	}
	switch args[1] {
	case "list":
		accounts, err := client.ListAccounts(ctx, accountv1.ListFilter{})
		if err != nil {
			return err
		}
		for _, account := range accounts {
			fmt.Fprintf(output, "%s\trev=%d\t%s\tcapabilities=%v\tbindings=%s\n",
				account.Ref.AccountID, account.Ref.Revision, account.Status,
				account.Capabilities, summarizeBindings(account.Bindings))
		}
		return nil
	case "show":
		if len(args) < 3 {
			return fmt.Errorf("usage: hub llm account show <account_id>")
		}
		account, err := client.Account(ctx, accountv1.AccountRef{AccountID: args[2]})
		if err != nil {
			return err
		}
		encoded, _ := json.MarshalIndent(account, "", "  ")
		fmt.Fprintln(output, string(encoded))
		return nil
	case "add":
		if len(args) < 4 || args[2] != "--file" {
			return fmt.Errorf("usage: hub llm account add --file accounts.jsonl")
		}
		accounts, err := LoadAccountFile(args[3])
		if err != nil {
			return err
		}
		for _, account := range accounts {
			created, err := client.CreateAccount(ctx, account)
			if err != nil {
				return err
			}
			fmt.Fprintf(output, "added %s rev=%d\n", created.Ref.AccountID, created.Ref.Revision)
		}
		return nil
	case "use":
		if len(args) < 4 {
			return fmt.Errorf("usage: hub llm account use <account_id> <capability>")
		}
		account, err := client.Account(ctx, accountv1.AccountRef{AccountID: args[2]})
		if err != nil {
			return err
		}
		updated, err := client.SetDefault(ctx, accountv1.SetDefault{
			Ref: account.Ref, Capability: accountv1.Capability(args[3]),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "default %s rev=%d\n", args[2], updated.Ref.Revision)
		return nil
	case "remove":
		if len(args) < 3 {
			return fmt.Errorf("usage: hub llm account remove <account_id>")
		}
		account, err := client.Account(ctx, accountv1.AccountRef{AccountID: args[2]})
		if err != nil {
			return err
		}
		return client.ArchiveAccount(ctx, account.Ref)
	case "validate", "test":
		if len(args) >= 4 && args[2] == "--file" {
			reports, err := ValidateAccountFile(args[3])
			if err != nil {
				return err
			}
			for index, report := range reports {
				fmt.Fprintf(output, "account[%d] valid=%v issues=%v\n", index, report.Valid, report.Issues)
			}
			return nil
		}
		if len(args) < 3 {
			return fmt.Errorf("usage: hub llm account validate <account_id>|--file <path>")
		}
		account, err := client.Account(ctx, accountv1.AccountRef{AccountID: args[2]})
		if err != nil {
			return err
		}
		report, err := client.ValidateAccount(ctx, accountv1.ValidateAccount{Ref: account.Ref})
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "valid=%v issues=%v\n", report.Valid, report.Issues)
		return nil
	case "smoke":
		return runSmokeCLI(ctx, client, args[2:], output)
	default:
		return fmt.Errorf("unknown account command %q", args[1])
	}
}

func WaitForTurn(ctx context.Context, client *Client, sessionID string, after int64, output io.Writer) error {
	_, err := waitForMessageTurn(ctx, client, sessionID, "", after, output)
	return err
}

// WaitForMessageTurn waits for the turn that consumes one accepted message.
// Correlating message_id with turn_id prevents an already-running queued turn
// from being mistaken for the caller's turn.
func WaitForMessageTurn(ctx context.Context, client *Client, sessionID, messageID string, after int64, output io.Writer) error {
	_, err := waitForMessageTurn(ctx, client, sessionID, messageID, after, output)
	return err
}

func waitForMessageTurn(ctx context.Context, client *Client, sessionID, messageID string, after int64, output io.Writer) (int64, error) {
	deadline := time.Now().Add(3 * time.Minute)
	cursor := after
	turnID := ""
	consecutiveFailures := 0
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return cursor, fmt.Errorf("timed out waiting for turn")
		}
		waitMS := int(min(int64(15000), remaining.Milliseconds()))
		if waitMS <= 0 {
			return cursor, fmt.Errorf("timed out waiting for turn")
		}
		response, err := client.Watch(ctx, sessionv1.WatchRequest{
			SessionID: sessionID, AfterSeq: cursor, Limit: 1000, WaitMS: waitMS,
		})
		if err != nil {
			if ctx.Err() != nil {
				return cursor, ctx.Err()
			}
			if !retryableWatchError(err) {
				return cursor, err
			}
			consecutiveFailures++
			if consecutiveFailures >= 8 {
				return cursor, fmt.Errorf("watch reconnect failed: %w", err)
			}
			if err := waitContext(ctx, time.Duration(1<<min(consecutiveFailures, 5))*50*time.Millisecond); err != nil {
				return cursor, err
			}
			continue
		}
		consecutiveFailures = 0
		if response.ResyncRequired {
			snapshot, snapshotErr := client.Snapshot(ctx, sessionID, 1000)
			if snapshotErr != nil {
				return cursor, snapshotErr
			}
			response.Events = snapshot.Events
			response.Cursor = snapshot.Watermark
			response.Watermark = snapshot.Watermark
		}
		done, nextCursor, nextTurnID := consumeTurnEvents(
			response.Events, cursor, messageID, turnID, output,
		)
		cursor, turnID = nextCursor, nextTurnID
		if done {
			return cursor, nil
		}
	}
}

func consumeTurnEvents(events []sessionv1.EventView, cursor int64, messageID, turnID string, output io.Writer) (bool, int64, string) {
	for _, event := range events {
		if event.Seq <= cursor {
			continue
		}
		cursor = event.Seq
		switch event.Type {
		case "user.message.consumed":
			var payload struct {
				MessageID string `json:"message_id"`
				TurnID    string `json:"turn_id"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			if messageID == "" || payload.MessageID == messageID {
				turnID = payload.TurnID
			}
		case "assistant.message.completed":
			var payload struct {
				TurnID string `json:"turn_id"`
				Text   string `json:"text"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			if messageID == "" || (turnID != "" && payload.TurnID == turnID) {
				fmt.Fprintln(output, sanitizeTerminal(payload.Text))
			}
		case "tool.requested":
			printToolEvent(output, event, "requested")
		case "tool.completed":
			printToolEvent(output, event, "completed")
		case "tool.failed":
			printToolEvent(output, event, "failed")
		case "llm.failed":
			var payload struct {
				ReasonCode string `json:"reason_code"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			fmt.Fprintf(output, "[llm] failed code=%s seq=%d\n", payload.ReasonCode, event.Seq)
		case "turn.completed":
			var terminal struct {
				TurnID     string `json:"turn_id"`
				Status     string `json:"status"`
				ReasonCode string `json:"reason_code"`
			}
			_ = json.Unmarshal(event.Payload, &terminal)
			if messageID != "" {
				if turnID == "" || terminal.TurnID != turnID {
					continue
				}
			}
			if terminal.Status != "" && terminal.Status != "succeeded" {
				fmt.Fprintf(output, "[turn] %s reason=%s seq=%d\n",
					terminal.Status, terminal.ReasonCode, event.Seq)
			}
			return true, cursor, turnID
		}
	}
	return false, cursor, turnID
}

func printToolEvent(output io.Writer, event sessionv1.EventView, phase string) {
	var payload struct {
		ToolName string `json:"tool_name"`
		CallID   string `json:"call_id"`
		Timing   struct {
			TotalMS int64 `json:"total_ms"`
		} `json:"timing"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(event.Payload, &payload)
	callID := payload.CallID
	if callID == "" {
		callID = event.CallID
	}
	fmt.Fprintf(output, "[tool] %s name=%s call_id=%s seq=%d",
		phase, payload.ToolName, callID, event.Seq)
	if phase != "requested" {
		fmt.Fprintf(output, " total_ms=%d", payload.Timing.TotalMS)
	}
	if payload.Error != nil {
		fmt.Fprintf(output, " code=%s", payload.Error.Code)
	}
	fmt.Fprintln(output)
}

func retryableWatchError(err error) bool {
	var coded *sessionruntime.CodedError
	if errors.As(err, &coded) {
		return coded.Retryable
	}
	return true
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sanitizeTerminal(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return r
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, value)
	return strings.ToValidUTF8(cleaned, "\uFFFD")
}

const hubHelp = `vibe_agent hub commands:
  status
  daemon-shutdown
  session create|list|inspect|stop
  run|chat <session_id> <text>
  tool list|call
  compress <session_id> [--auto|--through-seq N]
  events <session_id> [--after N] [--limit N] [--wait-ms N]
  snapshot <session_id> [--tail-limit N]
  history <session_id> [--before-seq N] [--limit N]
  trace <session_id> [--call-id ID]
  stats <session_id> [--tool NAME] [--mode model|direct]
  llm account list|show|add|use|remove|validate|smoke`
