package larkcli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerUsesAndCleansIsolatedWorkingDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("runner-artifact-%d", time.Now().UnixNano())
	result, err := (ExecRunner{}).Run(context.Background(), CommandSpec{
		Executable: executable,
		Args:       []string{"-test.run=TestExecRunnerHelperProcess"},
		Env:        []string{"SFS_RUNNER_HELPER=1", "SFS_RUNNER_HELPER_FILE=" + name},
		Timeout:    5 * time.Second,
	})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	workingDir := strings.TrimSpace(string(result.Stdout))
	if workingDir == "" {
		t.Fatal("helper did not report its working directory")
	}
	if _, err := os.Stat(workingDir); !os.IsNotExist(err) {
		t.Fatalf("isolated working directory still exists: %v", err)
	}
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(current, name)); !os.IsNotExist(err) {
		t.Fatalf("runner wrote into caller working directory: %v", err)
	}
}

func TestExecRunnerPreservesCallerOwnedWorkingDirectory(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "artifact.txt"
	result, err := (ExecRunner{}).Run(context.Background(), CommandSpec{
		Executable: executable,
		Args:       []string{"-test.run=TestExecRunnerHelperProcess"},
		Env:        []string{"SFS_RUNNER_HELPER=1", "SFS_RUNNER_HELPER_FILE=" + name},
		Dir:        dir,
		Timeout:    5 * time.Second,
	})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("caller-owned output missing: %v", err)
	}
}

func TestExecRunnerHelperProcess(t *testing.T) {
	if os.Getenv("SFS_RUNNER_HELPER") != "1" {
		return
	}
	name := os.Getenv("SFS_RUNNER_HELPER_FILE")
	if name == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(name, []byte("generated"), 0o600); err != nil {
		os.Exit(3)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		os.Exit(4)
	}
	_, _ = fmt.Fprint(os.Stdout, workingDir)
	os.Exit(0)
}
