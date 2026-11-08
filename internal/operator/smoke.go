package operator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

type SmokeCase struct {
	CaseID     string               `json:"case_id"`
	AccountID  string               `json:"account_id"`
	Capability accountv1.Capability `json:"capability"`
	Enabled    bool                 `json:"enabled"`
	Tags       []string             `json:"tags,omitempty"`
	Timeout    string               `json:"timeout,omitempty"`
	Input      map[string]any       `json:"input,omitempty"`
	Assertions map[string]any       `json:"assertions,omitempty"`
}

type smokeCaseRecord struct {
	SchemaVersion int                  `json:"schema_version"`
	CaseID        string               `json:"case_id"`
	AccountID     string               `json:"account_id"`
	Capability    accountv1.Capability `json:"capability"`
	Enabled       *bool                `json:"enabled"`
	Tags          []string             `json:"tags,omitempty"`
	Timeout       string               `json:"timeout,omitempty"`
	Input         map[string]any       `json:"input,omitempty"`
	Assertions    map[string]any       `json:"assertions,omitempty"`
}

func LoadSmokeCases(path string) ([]SmokeCase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var result []SmokeCase
	seen := make(map[string]bool)
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var record smokeCaseRecord
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return nil, fmt.Errorf("line %d: trailing JSON value", line)
		}
		if record.SchemaVersion != 1 {
			return nil, fmt.Errorf("line %d: unsupported schema version", line)
		}
		if record.CaseID == "" || record.AccountID == "" || !record.Capability.Valid() {
			return nil, fmt.Errorf("line %d: case_id, account_id and capability are required", line)
		}
		if seen[record.CaseID] {
			return nil, fmt.Errorf("line %d: duplicate case_id %q", line, record.CaseID)
		}
		seen[record.CaseID] = true
		enabled := true
		if record.Enabled != nil {
			enabled = *record.Enabled
		}
		result = append(result, SmokeCase{
			CaseID: record.CaseID, AccountID: record.AccountID,
			Capability: record.Capability, Enabled: enabled, Tags: record.Tags,
			Timeout: record.Timeout, Input: record.Input, Assertions: record.Assertions,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func runSmokeCLI(ctx context.Context, client *Client, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("smoke", flag.ContinueOnError)
	file := flags.String("file", "", "smoke cases JSONL")
	tag := flags.String("tag", "", "required case tag")
	offline := flags.Bool("offline", false, "validate cases without provider calls")
	accountID := flags.String("account", "", "account id")
	capability := flags.String("capability", "", "capability")
	if err := flags.Parse(args); err != nil {
		return err
	}
	positional := flags.Args()
	if *file != "" && len(positional) > 0 {
		return fmt.Errorf("usage: hub llm account smoke --file <path> [--tag TAG] [--offline]")
	}
	if *file == "" {
		if *accountID == "" && len(positional) > 0 {
			*accountID = positional[0]
		}
		if *capability == "" && len(positional) > 1 {
			*capability = positional[1]
		}
		if *accountID == "" || *capability == "" {
			return fmt.Errorf("usage: hub llm account smoke <account_id> <capability> | --file <path> [--tag TAG] [--offline]")
		}
		return runOneSmoke(ctx, client, SmokeCase{
			CaseID: "cli", AccountID: *accountID, Capability: accountv1.Capability(*capability),
		}, *offline, output)
	}
	cases, err := LoadSmokeCases(*file)
	if err != nil {
		return err
	}
	selected := 0
	var failures []error
	for _, smokeCase := range cases {
		if !smokeCase.Enabled || (*tag != "" && !containsTag(smokeCase.Tags, *tag)) {
			continue
		}
		selected++
		if err := runOneSmoke(ctx, client, smokeCase, *offline, output); err != nil {
			failures = append(failures, err)
		}
	}
	if selected == 0 {
		return errors.New("no enabled smoke cases matched the requested tag")
	}
	return errors.Join(failures...)
}

func runOneSmoke(ctx context.Context, client *Client, smokeCase SmokeCase, offline bool, output io.Writer) error {
	if !smokeCase.Capability.Valid() {
		return fmt.Errorf("%s: invalid capability %q", smokeCase.CaseID, smokeCase.Capability)
	}
	account, err := client.Account(ctx, accountv1.AccountRef{AccountID: smokeCase.AccountID})
	if err != nil {
		return fmt.Errorf("%s: %w", smokeCase.CaseID, err)
	}
	report, err := client.ValidateAccount(ctx, accountv1.ValidateAccount{Ref: account.Ref})
	if err != nil {
		return fmt.Errorf("%s: %w", smokeCase.CaseID, err)
	}
	if !report.Valid {
		return fmt.Errorf("%s: account %s is invalid: %v", smokeCase.CaseID, account.Ref.AccountID, report.Issues)
	}
	if !hasCapability(account.Capabilities, smokeCase.Capability) {
		return fmt.Errorf("%s: account %s lacks capability %s", smokeCase.CaseID, account.Ref.AccountID, smokeCase.Capability)
	}
	timeoutMS, err := parseSmokeTimeout(smokeCase.Timeout)
	if err != nil {
		return fmt.Errorf("%s: %w", smokeCase.CaseID, err)
	}
	if offline {
		fmt.Fprintf(output, "offline case=%s account=%s capability=%s valid=true\n",
			smokeCase.CaseID, smokeCase.AccountID, smokeCase.Capability)
		return nil
	}
	result, err := client.SmokeAccount(ctx, accountv1.SmokeRequest{
		Account: account.Ref, Capability: smokeCase.Capability, CaseID: smokeCase.CaseID,
		Input: smokeCase.Input, TimeoutMS: timeoutMS,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", smokeCase.CaseID, err)
	}
	fmt.Fprintf(output, "case=%s account=%s capability=%s passed=%v code=%s duration_ms=%d\n",
		smokeCase.CaseID, smokeCase.AccountID, smokeCase.Capability,
		result.Passed, result.ErrorCode, result.DurationMS)
	if !result.Passed {
		return fmt.Errorf("%s: smoke failed with %s", smokeCase.CaseID, result.ErrorCode)
	}
	return nil
}

func parseSmokeTimeout(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return 0, fmt.Errorf("invalid smoke timeout %q", value)
	}
	return timeout.Milliseconds(), nil
}

func containsTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if strings.EqualFold(tag, wanted) {
			return true
		}
	}
	return false
}

func hasCapability(values []accountv1.Capability, wanted accountv1.Capability) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
