package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AllenReder/tmh/internal/agenttools"
	"github.com/AllenReder/tmh/internal/config"
	"github.com/AllenReder/tmh/internal/generator"
	"github.com/AllenReder/tmh/internal/openai"
	"github.com/AllenReder/tmh/internal/safety"
)

var Version = "dev"

const maxPromptBytes = 64 * 1024

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"tmh"}
	}
	invokedAsAgent := filepath.Base(args[0]) == "tmha"
	if len(args) > 1 && args[1] == "config" {
		return runConfig(ctx, args[2:], stdout, stderr)
	}
	return runGenerate(ctx, args[1:], invokedAsAgent, stdin, stdout, stderr)
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func runGenerate(ctx context.Context, args []string, invokedAsAgent bool, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tmh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { printUsage(stderr) }
	agentMode := fs.Bool("agent", invokedAsAgent, "allow bounded read-only file inspection")
	baseURL := fs.String("base-url", "", "override the OpenAI-compatible base URL")
	model := fs.String("model", "", "override the configured model")
	timeout := fs.Duration("timeout", 0, "override the total request timeout")
	debug := fs.Bool("debug", false, "print redacted diagnostics to stderr")
	showVersion := fs.Bool("version", false, "print version and exit")
	var allowedPaths stringList
	fs.Var(&allowedPaths, "allow-path", "allow agent file access to an additional path (repeatable)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, Version)
		return 0
	}
	if fs.NArg() == 1 {
		switch fs.Arg(0) {
		case "help":
			printUsage(stdout)
			return 0
		case "version":
			fmt.Fprintln(stdout, Version)
			return 0
		}
	}
	if invokedAsAgent {
		*agentMode = true
	}
	if len(allowedPaths) > 0 && !*agentMode {
		fmt.Fprintln(stderr, "Error: --allow-path requires --agent or tmha")
		return 2
	}

	query, err := readQuery(fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	cfg, err := config.Load(config.Overrides{BaseURL: *baseURL, Model: *model}, true)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	requestTimeout := cfg.TMHTimeout
	if *agentMode {
		requestTimeout = cfg.TMHATimeout
	}
	if *timeout != 0 {
		if *timeout < time.Second || *timeout > 30*time.Minute {
			fmt.Fprintln(stderr, "Error: --timeout must be between 1s and 30m")
			return 2
		}
		requestTimeout = *timeout
	}

	runCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	client := &openai.Client{
		BaseURL:     cfg.BaseURL,
		APIKey:      cfg.APIKey,
		HTTPClient:  &http.Client{},
		Debug:       *debug,
		DebugWriter: stderr,
		Version:     Version,
	}
	gen := &generator.Generator{Client: client, Model: cfg.Model}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "Error: get current directory: %v\n", err)
		return 1
	}

	var result generator.Result
	if *agentMode {
		fileTools, toolErr := agenttools.New(cwd, allowedPaths, func(format string, values ...any) {
			fmt.Fprintf(stderr, format+"\n", values...)
		})
		if toolErr != nil {
			fmt.Fprintf(stderr, "Error: %v\n", toolErr)
			return 2
		}
		result, err = gen.Agent(runCtx, query, cwd, fileTools)
	} else {
		result, err = gen.Direct(runCtx, query, cwd)
	}
	if err != nil {
		if errors.Is(runCtx.Err(), context.Canceled) {
			fmt.Fprintln(stderr, "Error: request canceled")
			return 130
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "Error: request exceeded %s\n", requestTimeout)
			return 1
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if strings.TrimSpace(result.Command) == "" {
		if result.Explanation != "" {
			fmt.Fprintf(stderr, "Error: %s\n", result.Explanation)
		} else {
			fmt.Fprintln(stderr, "Error: model returned no command")
		}
		return 1
	}

	fmt.Fprintf(stderr, "Explanation: %s\n", result.Explanation)
	for _, risk := range safety.Classify(result.Command) {
		fmt.Fprintf(stderr, "Warning [%s]: %s. Review before running.\n", risk.Category, risk.Message)
	}
	fmt.Fprintln(stdout, result.Command)
	return 0
}

func runConfig(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "show" && args[0] != "test") {
		fmt.Fprintln(stderr, "Usage: tmh config <show|test> [--base-url URL] [--model MODEL] [--timeout DURATION]")
		return 2
	}
	action := args[0]
	fs := flag.NewFlagSet("tmh config "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "", "override the OpenAI-compatible base URL")
	model := fs.String("model", "", "override the configured model")
	timeout := fs.Duration("timeout", 0, "override the connection test timeout")
	debug := fs.Bool("debug", false, "print redacted diagnostics to stderr")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "Error: unexpected config arguments")
		return 2
	}
	cfg, err := config.Load(config.Overrides{BaseURL: *baseURL, Model: *model}, action == "test")
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	if action == "show" {
		showConfig(stdout, cfg)
		return 0
	}

	testTimeout := cfg.TMHTimeout
	if *timeout != 0 {
		testTimeout = *timeout
	}
	if testTimeout < time.Second || testTimeout > 30*time.Minute {
		fmt.Fprintln(stderr, "Error: --timeout must be between 1s and 30m")
		return 2
	}
	testCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	client := &openai.Client{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, HTTPClient: &http.Client{}, Debug: *debug, DebugWriter: stderr, Version: Version}
	message, err := client.Complete(testCtx, openai.Request{
		Model: cfg.Model,
		Messages: []openai.Message{
			{Role: "system", Content: "This is a connectivity test. Reply with OK."},
			{Role: "user", Content: "OK"},
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "Error: configuration test failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Connection successful. Model returned %d characters.\n", len(message.Content))
	return 0
}

func showConfig(w io.Writer, cfg config.Config) {
	keyState := "not configured"
	if cfg.APIKey != "" {
		keyState = "configured via " + cfg.Sources["api_key"]
	}
	fmt.Fprintf(w, "config_file = %s\n", cfg.Path)
	fmt.Fprintf(w, "base_url = %s (%s)\n", cfg.BaseURL, cfg.Sources["base_url"])
	model := cfg.Model
	if model == "" {
		model = "<unset>"
	}
	fmt.Fprintf(w, "model = %s (%s)\n", model, cfg.Sources["model"])
	fmt.Fprintf(w, "tmh_timeout = %s (%s)\n", cfg.TMHTimeout, cfg.Sources["tmh_timeout"])
	fmt.Fprintf(w, "tmha_timeout = %s (%s)\n", cfg.TMHATimeout, cfg.Sources["tmha_timeout"])
	fmt.Fprintf(w, "api_key = %s\n", keyState)
}

func readQuery(args []string, stdin io.Reader) (string, error) {
	if len(args) > 0 {
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			return "", fmt.Errorf("request is empty")
		}
		if len(query) > maxPromptBytes {
			return "", fmt.Errorf("request exceeds %d bytes", maxPromptBytes)
		}
		return query, nil
	}
	if isTerminalReader(stdin) {
		return "", fmt.Errorf("request is required; pass text as arguments or pipe it on stdin")
	}
	data, err := io.ReadAll(io.LimitReader(stdin, maxPromptBytes+1))
	if err != nil {
		return "", fmt.Errorf("read request from stdin: %w", err)
	}
	if len(data) > maxPromptBytes {
		return "", fmt.Errorf("request exceeds %d bytes", maxPromptBytes)
	}
	query := strings.TrimSpace(string(data))
	if query == "" {
		return "", fmt.Errorf("request is empty")
	}
	return query, nil
}

func isTerminalReader(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  tmh [flags] <natural-language request>")
	fmt.Fprintln(w, "  tmh --agent [--allow-path PATH]... <request>")
	fmt.Fprintln(w, "  tmha [--allow-path PATH]... <request>")
	fmt.Fprintln(w, "  tmh config <show|test>")
}
