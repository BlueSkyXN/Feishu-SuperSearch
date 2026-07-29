package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/internal/planvalidate"
	"github.com/BlueSkyXN/Feishu-SuperSearch/kernel"
	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
)

// Planner delegates planning to an external executable. The executable reads a
// single JSON object from stdin and writes a RetrievalPlan JSON object to stdout.
// This is the language-neutral integration point for LLM planners and SKILL hosts.
type Planner struct {
	Command        string
	Args           []string
	Timeout        time.Duration
	MaxStdoutBytes int64
	MaxStderrBytes int64
}

func (p Planner) Name() string { return "external-command" }
func (p Planner) Plan(ctx context.Context, r planner.UserRequest, caps kernel.CapabilitySnapshot) (kernel.RetrievalPlan, error) {
	if p.Command == "" {
		return kernel.RetrievalPlan{}, fmt.Errorf("planner command is required")
	}
	if p.Timeout <= 0 {
		p.Timeout = 30 * time.Second
	}
	if p.MaxStdoutBytes <= 0 {
		p.MaxStdoutBytes = 4 << 20
	}
	if p.MaxStderrBytes <= 0 {
		p.MaxStderrBytes = 1 << 20
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	input := map[string]any{"request": r, "capabilities": caps, "output_schema": "retrieval-plan/v1"}
	b, _ := json.Marshal(input)
	cmd := exec.CommandContext(ctx, p.Command, p.Args...)
	cmd.Stdin = bytes.NewReader(b)
	out := &limitedBuffer{limit: p.MaxStdoutBytes}
	errOut := &limitedBuffer{limit: p.MaxStderrBytes}
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return kernel.RetrievalPlan{}, &kernel.ErrorDetail{Type: kernel.ErrDeadlineExceeded, Message: "external planner timed out"}
		case errors.Is(ctx.Err(), context.Canceled):
			return kernel.RetrievalPlan{}, &kernel.ErrorDetail{Type: kernel.ErrCancelled, Message: "external planner cancelled"}
		default:
			return kernel.RetrievalPlan{}, fmt.Errorf("external planner failed: %w: %s", err, errOut.String())
		}
	}
	if out.truncated {
		return kernel.RetrievalPlan{}, &kernel.ErrorDetail{Type: kernel.ErrParse, Message: "external planner output exceeded configured size limit"}
	}
	if err := planvalidate.ValidateJSON(out.Bytes()); err != nil {
		return kernel.RetrievalPlan{}, fmt.Errorf("external planner returned invalid plan: %w", err)
	}
	var plan kernel.RetrievalPlan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		return plan, fmt.Errorf("external planner returned invalid plan JSON: %w", err)
	}
	return plan, nil
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.written
	if remaining > 0 {
		write := int64(len(p))
		if write > remaining {
			write = remaining
		}
		_, _ = b.buf.Write(p[:write])
		b.written += write
	}
	if int64(n) > remaining {
		b.truncated = true
	}
	return n, nil
}
func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }
func (b *limitedBuffer) String() string {
	s := b.buf.String()
	if b.truncated {
		s += "\n...[truncated]"
	}
	return s
}
