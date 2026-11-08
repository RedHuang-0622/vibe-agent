package calculator

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type Tool struct{}

func New() *Tool {
	return &Tool{}
}

func Spec() toolv1.ToolSpec {
	return toolv1.ToolSpec{
		Name:               "calculator",
		Version:            "v1",
		Kind:               "local",
		Description:        "Evaluate a deterministic arithmetic expression",
		Parameters:         json.RawMessage(`{"type":"object","required":["expression"],"properties":{"expression":{"type":"string"}}}`),
		DirectRequestField: "expression",
		ReadOnly:           true,
		TimeoutMS:          2000,
	}
}

func (t *Tool) Invoke(_ context.Context, call tooling.Call) (any, error) {
	var args struct {
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid calculator arguments", false)
	}
	value, err := Evaluate(args.Expression)
	if err != nil {
		return nil, err
	}
	return map[string]any{"value": value, "expression": strings.TrimSpace(args.Expression)}, nil
}

type Adapter struct{}

func (Adapter) Adapt(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "expression is required", false)
	}
	return json.Marshal(map[string]string{"expression": raw})
}

func Evaluate(expression string) (float64, error) {
	parser := &parser{input: expression}
	value, err := parser.parseExpression()
	if err != nil {
		return 0, err
	}
	parser.skipSpace()
	if parser.pos != len(parser.input) {
		return 0, tooling.NewError(tooling.CodeInvalidRequest, fmt.Sprintf("unexpected character at position %d", parser.pos), false)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, tooling.NewError(tooling.CodeInvalidRequest, "expression result is not finite", false)
	}
	return value, nil
}

type parser struct {
	input string
	pos   int
}

func (p *parser) parseExpression() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.consume('+') {
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			left += right
		} else if p.consume('-') {
			right, err := p.parseTerm()
			if err != nil {
				return 0, err
			}
			left -= right
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseTerm() (float64, error) {
	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.consume('*') {
			right, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			left *= right
		} else if p.consume('/') {
			right, err := p.parseFactor()
			if err != nil {
				return 0, err
			}
			if right == 0 {
				return 0, tooling.NewError(tooling.CodeInvalidRequest, "division by zero", false)
			}
			left /= right
		} else {
			return left, nil
		}
	}
}

func (p *parser) parseFactor() (float64, error) {
	p.skipSpace()
	if p.consume('+') {
		return p.parseFactor()
	}
	if p.consume('-') {
		value, err := p.parseFactor()
		return -value, err
	}
	if p.consume('(') {
		value, err := p.parseExpression()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if !p.consume(')') {
			return 0, tooling.NewError(tooling.CodeInvalidRequest, "missing closing parenthesis", false)
		}
		return value, nil
	}
	start := p.pos
	for p.pos < len(p.input) {
		r := rune(p.input[p.pos])
		if !unicode.IsDigit(r) && r != '.' {
			break
		}
		p.pos++
	}
	if start == p.pos {
		return 0, tooling.NewError(tooling.CodeInvalidRequest, "number expected", false)
	}
	value, err := strconv.ParseFloat(p.input[start:p.pos], 64)
	if err != nil {
		return 0, tooling.NewError(tooling.CodeInvalidRequest, "invalid number", false)
	}
	return value, nil
}

func (p *parser) skipSpace() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *parser) consume(want byte) bool {
	if p.pos < len(p.input) && p.input[p.pos] == want {
		p.pos++
		return true
	}
	return false
}
