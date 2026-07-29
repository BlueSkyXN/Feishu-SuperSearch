package larkcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type CommandSpec struct {
	Executable  string
	Args        []string
	Env         []string
	Stdin       []byte
	Dir         string
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

type CommandResult struct {
	ExitCode        int
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	Duration        time.Duration
}

type Runner interface {
	Run(context.Context, CommandSpec) (CommandResult, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	var out CommandResult
	if spec.Executable == "" {
		return out, fmt.Errorf("executable is required")
	}
	if spec.Timeout <= 0 {
		spec.Timeout = 10 * time.Second
	}
	if spec.StdoutLimit <= 0 {
		spec.StdoutLimit = 16 << 20
	}
	if spec.StderrLimit <= 0 {
		spec.StderrLimit = 4 << 20
	}
	executable, err := exec.LookPath(spec.Executable)
	if err != nil {
		return out, err
	}
	if !filepath.IsAbs(executable) {
		executable, err = filepath.Abs(executable)
		if err != nil {
			return out, err
		}
	}
	workingDir := spec.Dir
	if workingDir == "" {
		workingDir, err = os.MkdirTemp("", "sfs-larkcli-")
		if err != nil {
			return out, fmt.Errorf("create isolated lark-cli directory: %w", err)
		}
		defer os.RemoveAll(workingDir)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, executable, spec.Args...)
	cmd.Dir = workingDir
	if len(spec.Env) > 0 {
		cmd.Env = append(cmd.Environ(), spec.Env...)
	}
	if len(spec.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}
	stdout := &limitedBuffer{limit: spec.StdoutLimit}
	stderr := &limitedBuffer{limit: spec.StderrLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	started := time.Now()
	err = cmd.Run()
	out.Duration = time.Since(started)
	out.Stdout = stdout.Bytes()
	out.Stderr = stderr.Bytes()
	out.StdoutTruncated = stdout.truncated
	out.StderrTruncated = stderr.truncated
	if cmd.ProcessState != nil {
		out.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		out.ExitCode = -1
	}
	if cmdCtx.Err() != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			return out, fmt.Errorf("command timed out after %s: %w", spec.Timeout, context.DeadlineExceeded)
		}
		return out, cmdCtx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return out, nil
		}
		return out, err
	}
	return out, nil
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	n         int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.n
	if remaining > 0 {
		write := int64(len(p))
		if write > remaining {
			write = remaining
		}
		_, _ = b.buf.Write(p[:write])
		b.n += write
	}
	if int64(len(p)) > remaining {
		b.truncated = true
	}
	return n, nil
}
func (b *limitedBuffer) Bytes() []byte { return append([]byte(nil), b.buf.Bytes()...) }

func ExitSignal(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if status, ok := ee.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return status.Signal().String()
		}
	}
	return ""
}

func SanitizedCommand(exe string, args []string) string {
	parts := []string{exe}
	for _, a := range args {
		if strings.Contains(strings.ToLower(a), "token") || strings.Contains(strings.ToLower(a), "secret") {
			parts = append(parts, "<redacted>")
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}

var _ io.Writer = (*limitedBuffer)(nil)
