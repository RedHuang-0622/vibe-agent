package operator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"vibe-agent/internal/operator/sessioncache"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

func RunTUI(ctx context.Context, client *Client, input io.Reader, output io.Writer) error {
	fmt.Fprintln(output, "vibe_agent TUI. Type /help for commands.")
	sessionID, err := defaultSession(ctx, client)
	if err != nil {
		return err
	}
	view := sessioncache.New()
	lastSeq, err := refreshView(ctx, client, view, sessionID, 100)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for {
		fmt.Fprintf(output, "[%s] > ", sessionID)
		if !scanner.Scan() {
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			return nil
		}
		if strings.HasPrefix(line, "/") && !strings.HasPrefix(line, "//") {
			previousSession := sessionID
			nextSession, nextSeq, stop, err := handleTUICommand(ctx, client, view, sessionID, line, output)
			if err != nil {
				fmt.Fprintln(output, "error:", err)
				continue
			}
			if stop {
				return nil
			}
			if nextSession != "" && nextSession != previousSession {
				sessionID = nextSession
				lastSeq = nextSeq
				continue
			}
			if nextSeq > lastSeq {
				if view.Watermark() < nextSeq || view.NeedsResync() {
					if err := syncView(ctx, client, view, sessionID, view.Watermark()); err != nil {
						fmt.Fprintln(output, "refresh warning:", err)
					}
				}
				lastSeq = nextSeq
			}
			if view.NeedsResync() {
				if _, err := refreshView(ctx, client, view, sessionID, 1000); err != nil {
					fmt.Fprintln(output, "refresh warning:", err)
				}
			}
			continue
		}
		message := line
		if strings.HasPrefix(message, "//") {
			message = strings.TrimPrefix(message, "/")
		}
		submitted, err := client.Submit(ctx, sessionID, message, "queue")
		if err != nil {
			fmt.Fprintln(output, "submit error:", err)
			continue
		}
		nextSeq, err := waitForMessageTurn(ctx, client, sessionID, submitted.MessageID, lastSeq, output)
		if err != nil {
			fmt.Fprintln(output, "wait error:", err)
			continue
		}
		lastSeq = nextSeq
		if err := syncView(ctx, client, view, sessionID, view.Watermark()); err != nil {
			fmt.Fprintln(output, "refresh warning:", err)
		}
	}
}

func handleTUICommand(ctx context.Context, client *Client, view *sessioncache.Cache, sessionID, line string, output io.Writer) (string, int64, bool, error) {
	command, rest, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	switch command {
	case "help":
		printTUIHelp(output)
		return "", 0, false, nil
	case "sessions":
		sessions, err := client.ListSessions(ctx)
		if err != nil {
			return "", 0, false, err
		}
		for _, session := range sessions {
			fmt.Fprintf(output, "%s\t%s\t%s\tseq=%d\tpid=%d\n",
				session.SessionID, session.Name, session.State, session.LastSeq, session.PID)
		}
		return "", 0, false, nil
	case "new":
		session, err := client.CreateSession(ctx, strings.TrimSpace(rest))
		if err != nil {
			return "", 0, false, err
		}
		fmt.Fprintln(output, "created:", session.SessionID)
		seq, err := refreshView(ctx, client, view, session.SessionID, 100)
		return session.SessionID, seq, false, err
	case "use":
		target := strings.TrimSpace(rest)
		if target == "" {
			return "", 0, false, fmt.Errorf("usage: /use <session_id>")
		}
		seq, err := refreshView(ctx, client, view, target, 100)
		return target, seq, false, err
	case "snapshot":
		seq, err := refreshView(ctx, client, view, sessionID, 100)
		if err != nil {
			return "", 0, false, err
		}
		printWindow(output, view)
		return "", seq, false, nil
	case "history":
		if view.WindowFirstSeq() == 0 || view.NeedsResync() {
			_, err := refreshView(ctx, client, view, sessionID, 100)
			if err != nil {
				return "", 0, false, err
			}
		}
		printWindow(output, view)
		return "", view.Watermark(), false, nil
	case "older":
		limit := 100
		if value, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && value > 0 {
			limit = min(value, 1000)
		}
		if view.WindowFirstSeq() == 0 || view.NeedsResync() {
			if _, err := refreshView(ctx, client, view, sessionID, 100); err != nil {
				return "", 0, false, err
			}
		}
		before := view.WindowFirstSeq()
		if before == 0 {
			fmt.Fprintln(output, "no history")
			return "", 0, false, nil
		}
		history, err := client.History(ctx, sessionv1.SessionHistoryRequest{
			SessionID: sessionID, BeforeSeq: before, Limit: limit,
		})
		if err != nil {
			return "", 0, false, err
		}
		if !view.ApplyHistory(history) {
			if _, err := refreshView(ctx, client, view, sessionID, 100); err != nil {
				return "", 0, false, err
			}
		}
		printWindow(output, view)
		return "", view.Watermark(), false, nil
	case "trace":
		result, err := client.Trace(ctx, sessionv1.TraceRequest{SessionID: sessionID, CallID: strings.TrimSpace(rest)})
		if err != nil {
			return "", 0, false, err
		}
		for _, node := range result.Nodes {
			fmt.Fprintf(output, "%d\t%s\t%s\t%s\n", node.Seq, node.Type, node.CallID, node.Status)
		}
		return "", result.Watermark, false, nil
	case "stats":
		result, err := client.Stats(ctx, sessionv1.StatsRequest{SessionID: sessionID})
		if err != nil {
			return "", 0, false, err
		}
		for _, stat := range result.ByTool {
			fmt.Fprintf(output, "%s/%s/%s calls=%d failed=%d p95=%.1fms\n",
				stat.ToolName, stat.ToolKind, stat.InvocationMode,
				stat.Calls, stat.Failed, stat.Total.P95MS)
		}
		return "", result.Watermark, false, nil
	case "accounts":
		accounts, err := client.ListAccounts(ctx, accountv1.ListFilter{})
		if err != nil {
			return "", 0, false, err
		}
		for _, account := range accounts {
			fmt.Fprintf(output, "%s\trev=%d\t%s\tcapabilities=%v\tbindings=%s\n",
				account.Ref.AccountID, account.Ref.Revision, account.Status,
				account.Capabilities, summarizeBindings(account.Bindings))
		}
		return "", 0, false, nil
	case "compress":
		request := sessionv1.CompressionRequest{SessionID: sessionID, Trigger: "external"}
		if rest == "auto" || strings.TrimSpace(rest) == "" {
			request.Trigger = "autonomous"
		} else if value, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64); err == nil {
			request.ThroughSeq = value
		}
		result, err := client.Compress(ctx, request)
		if err != nil {
			return "", 0, false, err
		}
		fmt.Fprintf(output, "compressed=%v red_line=%d error=%s\n", result.Committed, result.RedLineSeq, result.ErrorCode)
		session, err := client.InspectSession(ctx, sessionID)
		return "", session.LastSeq, false, err
	case "cancel":
		canceled, err := client.Cancel(ctx, sessionID)
		fmt.Fprintln(output, "canceled:", canceled)
		if err != nil {
			return "", 0, false, err
		}
		session, err := client.InspectSession(ctx, sessionID)
		return "", session.LastSeq, false, err
	case "tools":
		result, err := client.Tools(ctx, sessionID)
		if err != nil {
			return "", 0, false, err
		}
		for _, tool := range result.Tools {
			fmt.Fprintf(output, "%s\t%s\t%s\n", tool.Name, tool.Kind, tool.Description)
		}
		return "", 0, false, nil
	case "board":
		request := strings.TrimSpace(rest)
		if request == "" || request == "list" {
			request = `{"action":"list"}`
		}
		result, err := client.DirectTool(ctx, sessionID, "/todo_board "+request)
		if err != nil {
			return "", 0, false, err
		}
		if result.Error != nil {
			fmt.Fprintf(output, "board error: %s %s retryable=%v\n",
				result.Error.Code, sanitizeTerminal(result.Error.Message), result.Error.Retryable)
		} else {
			fmt.Fprintln(output, string(result.Result))
		}
		session, err := client.InspectSession(ctx, sessionID)
		return "", session.LastSeq, false, err
	default:
		result, err := client.DirectTool(ctx, sessionID, line)
		if err != nil {
			return "", 0, false, err
		}
		if result.Error != nil {
			fmt.Fprintf(output, "tool error: %s %s retryable=%v reset_at=%v\n",
				result.Error.Code, sanitizeTerminal(result.Error.Message), result.Error.Retryable, result.Error.ResetAt)
		} else {
			fmt.Fprintln(output, string(result.Result))
		}
		session, err := client.InspectSession(ctx, sessionID)
		return "", session.LastSeq, false, err
	}
}

func refreshView(ctx context.Context, client *Client, view *sessioncache.Cache, sessionID string, limit int) (int64, error) {
	snapshot, err := client.Snapshot(ctx, sessionID, limit)
	if err != nil {
		return 0, err
	}
	if !view.ApplySnapshot(snapshot) {
		return 0, fmt.Errorf("invalid snapshot for session %s", sessionID)
	}
	return snapshot.Watermark, nil
}

func syncView(ctx context.Context, client *Client, view *sessioncache.Cache, sessionID string, afterSeq int64) error {
	response, err := client.Watch(ctx, sessionv1.WatchRequest{
		SessionID: sessionID, AfterSeq: afterSeq, Limit: 1000,
	})
	if err != nil {
		return err
	}
	if response.ResyncRequired {
		_, err := refreshView(ctx, client, view, sessionID, 1000)
		return err
	}
	view.ApplyIncremental(response)
	return nil
}

func printWindow(output io.Writer, view *sessioncache.Cache) {
	events := view.Window()
	if len(events) == 0 {
		fmt.Fprintln(output, "no events")
		return
	}
	for _, event := range events {
		label := payloadText(event.Payload, "text")
		if label == "" {
			label = payloadText(event.Payload, "message_id")
		}
		fmt.Fprintf(output, "%d\t%s\t%s", event.Seq, event.Type, event.Status)
		if label != "" {
			fmt.Fprintf(output, "\t%s", label)
		}
		fmt.Fprintln(output)
	}
	running, known, turnID := view.TurnState()
	if known {
		fmt.Fprintf(output, "turn running=%v id=%s watermark=%d older_more=%v\n",
			running, turnID, view.Watermark(), view.OlderMore())
	} else {
		fmt.Fprintf(output, "turn state=unknown watermark=%d older_more=%v\n",
			view.Watermark(), view.OlderMore())
	}
}

func defaultSession(ctx context.Context, client *Client) (string, error) {
	sessions, err := client.ListSessions(ctx)
	if err != nil {
		return "", err
	}
	for _, session := range sessions {
		if session.State != sessionv1.StateStopped && session.State != sessionv1.StateFailed {
			return session.SessionID, nil
		}
	}
	created, err := client.CreateSession(ctx, "default")
	if err != nil {
		return "", err
	}
	return created.SessionID, nil
}

func payloadText(payload []byte, key string) string {
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return ""
	}
	text, _ := value[key].(string)
	return sanitizeTerminal(text)
}

func printTUIHelp(output io.Writer) {
	fmt.Fprintln(output, `/sessions /new <name> /use <id> /snapshot /history /older [limit]
/tools /board [list|JSON operation] /trace [call_id] /stats
/accounts /compress [seq|auto] /cancel /help /exit
Any other text is sent as chat; /<tool> args invokes a tool directly.`)
}
