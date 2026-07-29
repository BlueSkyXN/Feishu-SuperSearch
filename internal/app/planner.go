package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/BlueSkyXN/Feishu-SuperSearch/planner"
	commandplanner "github.com/BlueSkyXN/Feishu-SuperSearch/planner/command"
	defaultplanner "github.com/BlueSkyXN/Feishu-SuperSearch/planner/default"
	rulesplanner "github.com/BlueSkyXN/Feishu-SuperSearch/planner/rules"
)

// BuildPlanner resolves a planner implementation without introducing any LLM
// dependency into the retrieval kernel. The command planner is a language-
// neutral JSON stdin/stdout boundary suitable for an LLM host or SKILL runner.
func BuildPlanner(cfg PlannerConfig) (planner.Planner, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "", "rules":
		return rulesplanner.Planner{}, nil
	case "default", "fixed":
		return defaultplanner.Planner{}, nil
	case "command", "external", "llm":
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, fmt.Errorf("planner type %q requires planner.command", cfg.Type)
		}
		return commandplanner.Planner{Command: cfg.Command, Args: append([]string(nil), cfg.Args...), Timeout: parsePlannerTimeout(cfg.Timeout), MaxStdoutBytes: cfg.MaxStdoutBytes, MaxStderrBytes: cfg.MaxStderrBytes}, nil
	default:
		return nil, fmt.Errorf("unknown planner type %q", cfg.Type)
	}
}

func parsePlannerTimeout(value string) time.Duration {
	if value == "" {
		return 30 * time.Second
	}
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}
