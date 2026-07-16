package tool

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AllenReder/tmh/internal/model"
)

func TestScopeRejectsSensitiveAndEscapingPaths(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".envrc"), []byte("export API_KEY=value"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".env", ".envrc", "escape.txt", outsideFile} {
		if _, err := scope.Resolve(path); err == nil {
			t.Fatalf("expected %q to be denied", path)
		}
	}
}

func TestScopeRejectsSensitiveRootButAllowsOrdinaryTokenProject(t *testing.T) {
	base := t.TempDir()
	sensitiveRoot := filepath.Join(base, ".ssh")
	if err := os.Mkdir(sensitiveRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewScope(sensitiveRoot, nil); err == nil {
		t.Fatal("expected sensitive current directory to be rejected")
	}
	project := filepath.Join(base, "token-service")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(project, nil)
	if err != nil {
		t.Fatalf("ordinary project root was rejected: %v", err)
	}
	if _, err := scope.Resolve("README.md"); err != nil {
		t.Fatalf("file below ordinary token project was rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "api-token.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Resolve("api-token.txt"); err == nil {
		t.Fatal("expected token file to be rejected")
	}
}

func TestScopeRejectsSensitiveAllowedFileRoots(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".env.local", "secrets.json", "api-token.txt"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewScope(root, []string{path}); err == nil {
			t.Fatalf("sensitive allow-path file %q was accepted", name)
		}
	}
	ordinary := filepath.Join(root, "config.toml")
	if err := os.WriteFile(ordinary, []byte("ordinary"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(root, []string{ordinary})
	if err != nil {
		t.Fatalf("ordinary allow-path file was rejected: %v", err)
	}
	if _, err := scope.Resolve(ordinary); err != nil {
		t.Fatalf("ordinary allow-path file was unreadable: %v", err)
	}
	sensitiveAlias := filepath.Join(root, ".env.local")
	if err := os.Remove(sensitiveAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ordinary, sensitiveAlias); err != nil {
		t.Fatal(err)
	}
	if _, err := NewScope(root, []string{sensitiveAlias}); err == nil {
		t.Fatal("sensitive allow-path spelling was erased by symlink canonicalization")
	}
}

func TestScopePreservesStructuredSensitiveRootSemantics(t *testing.T) {
	root := t.TempDir()
	dockerRoot := filepath.Join(root, ".docker")
	gcloudRoot := filepath.Join(root, ".config", "gcloud")
	for _, directory := range []string{dockerRoot, gcloudRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := NewScope(root, []string{directory}); err == nil {
			t.Fatalf("structured sensitive directory %q was accepted", directory)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(dockerRoot, "config.json"):                          `{"auths":{"registry.invalid":{"auth":"private"}}}`,
		filepath.Join(gcloudRoot, "application_default_credentials.json"): `{"refresh_token":"private"}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewScope(root, []string{path}); err == nil {
			t.Fatalf("structured sensitive file %q was accepted", path)
		}
	}
	tokenProject := filepath.Join(root, "token-service")
	if err := os.Mkdir(tokenProject, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewScope(root, []string{tokenProject}); err != nil {
		t.Fatalf("ordinary token-service directory was rejected: %v", err)
	}

	bare := filepath.Join(root, "bare-repository")
	if err := os.MkdirAll(filepath.Join(bare, "objects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(bare, "config")
	if err := os.WriteFile(config, []byte("[core]\n\tbare = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewScope(bare, nil); err != nil {
		t.Fatalf("bare repository root should remain valid for Git inspection: %v", err)
	}
	for _, path := range []string{config, filepath.Join(bare, "objects")} {
		if _, err := NewScope(root, []string{path}); err == nil {
			t.Fatalf("bare repository descendant %q was accepted as allow-path", path)
		}
	}
}

func TestScopePreservesStructuredSensitivityBelowAllowedParent(t *testing.T) {
	root := t.TempDir()
	configRoot := filepath.Join(root, ".config")
	gcloud := filepath.Join(configRoot, "gcloud")
	if err := os.MkdirAll(gcloud, 0o700); err != nil {
		t.Fatal(err)
	}
	credential := filepath.Join(gcloud, "application_default_credentials.json")
	if err := os.WriteFile(credential, []byte(`{"refresh_token":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(root, []string{configRoot})
	if err != nil {
		t.Fatal(err)
	}
	if !scope.IsSensitive(gcloud) {
		t.Fatal("allowing .config erased the .config/gcloud sensitivity relationship")
	}
	if _, err := scope.Resolve(credential); err == nil {
		t.Fatal("gcloud credential below an allowed .config parent was readable")
	}
}

func TestRegistryAuditsPolicyBeforeExecution(t *testing.T) {
	events := make([]AuditEvent, 0)
	handler := &testHandler{result: Result{Status: StatusCompleted}}
	registry := NewRegistry(AuditFunc(func(event AuditEvent) { events = append(events, event) }), handler)
	result := registry.Execute(context.Background(), model.ToolCall{
		Type:     "function",
		Function: model.FunctionCall{Name: "test", Arguments: `{}`},
	})
	if result.Status != StatusCompleted || handler.runs != 1 {
		t.Fatalf("unexpected result: %+v runs=%d", result, handler.runs)
	}
	want := []AuditPhase{AuditRequested, AuditAllowed, AuditStarted, AuditCompleted}
	if len(events) != len(want) {
		t.Fatalf("unexpected events: %+v", events)
	}
	for index := range want {
		if events[index].Phase != want[index] {
			t.Fatalf("event %d = %s, want %s", index, events[index].Phase, want[index])
		}
	}
}

func TestRegistryDoesNotLogModelControlledUnknownToolNames(t *testing.T) {
	var audit bytes.Buffer
	registry := NewRegistry(NewWriterAudit(&audit), &testHandler{result: Result{Status: StatusCompleted}})
	result := registry.Execute(context.Background(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name: "unknown\nPrompt: secret\x1b[31m",
		},
	})
	if result.Status != StatusDenied || result.Code != CodeUnknownTool {
		t.Fatalf("unexpected unknown tool result: %+v", result)
	}
	output := audit.String()
	if strings.Contains(output, "Prompt") || strings.Contains(output, "secret") || strings.Contains(output, "\x1b") {
		t.Fatalf("audit leaked model-controlled tool name: %q", output)
	}
	if strings.Count(output, "\n") != 2 || !strings.Contains(output, "tool=<invalid> phase=requested") || !strings.Contains(output, "tool=<invalid> phase=blocked") {
		t.Fatalf("unexpected audit output: %q", output)
	}
}

func TestBudgetBatchReservationIsAtomic(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxToolCalls = 2
	budget := NewBudget(limits)
	three := []model.ToolCall{
		{Function: model.FunctionCall{Name: "one"}},
		{Function: model.FunctionCall{Name: "two"}},
		{Function: model.FunctionCall{Name: "three"}},
	}
	if err := budget.ReserveBatch(three); err == nil {
		t.Fatal("expected reservation failure")
	}
	if snapshot := budget.Snapshot(); snapshot.ToolCalls != 0 {
		t.Fatalf("failed batch consumed budget: %+v", snapshot)
	}
	if err := budget.ReserveBatch(three[:2]); err != nil {
		t.Fatal(err)
	}
}

func TestCommandBudgetReportsAccumulatedExhaustion(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxCommandOutputBytes = 4
	limits.MaxTotalCommandDuration = 10
	budget := NewBudget(limits)
	budget.Record("run_command", Result{Stdout: "1234", DurationMS: 1})
	if err := budget.CommandBudgetError(); err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("expected output budget exhaustion, got %v", err)
	}
}

func TestFileHandlersReturnTypedEnvelope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(nil, NewFileHandlers(scope)...)
	result := registry.Execute(context.Background(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name:      "read_text_file",
			Arguments: `{"path":"README.md"}`,
		},
	})
	if result.Status != StatusCompleted || !strings.Contains(result.JSON(), `"content":"hello"`) {
		t.Fatalf("unexpected file result: %s", result.JSON())
	}
}

func TestFileHandlersDenyGitMetadataAndFilterListings(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		".git/config":   "[core]\n\trepositoryformatversion = 0\n",
		".gitmodules":   "[submodule \"private\"]\n\turl = ssh://example.invalid/private\n",
		".envrc":        "export API_KEY=private\n",
		"README.md":     "visible\n",
		"worktree/.git": "gitdir: ../.git/worktrees/example\n",
	} {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scope, err := NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(nil, NewFileHandlers(scope)...)
	read := func(path string) Result {
		return registry.Execute(t.Context(), model.ToolCall{
			Type: "function",
			Function: model.FunctionCall{
				Name:      "read_text_file",
				Arguments: `{"path":` + quoted(path) + `}`,
			},
		})
	}
	for _, path := range []string{".git/config", ".gitmodules", "worktree/.git"} {
		result := read(path)
		if result.Status != StatusDenied || result.Code != CodeSensitivePath {
			t.Fatalf("sensitive Git path %q was not denied: %s", path, result.JSON())
		}
	}

	list := func(path string) directoryResult {
		t.Helper()
		result := registry.Execute(t.Context(), model.ToolCall{
			Type: "function",
			Function: model.FunctionCall{
				Name:      "list_directory",
				Arguments: `{"path":` + quoted(path) + `}`,
			},
		})
		if result.Status != StatusCompleted {
			t.Fatalf("list %q failed: %s", path, result.JSON())
		}
		listing, ok := result.Data.(directoryResult)
		if !ok {
			t.Fatalf("list %q returned unexpected data: %#v", path, result.Data)
		}
		return listing
	}
	rootListing := list(".")
	if !rootListing.Filtered || directoryHasName(rootListing, ".git") || directoryHasName(rootListing, ".gitmodules") || directoryHasName(rootListing, ".envrc") {
		t.Fatalf("root listing exposed Git metadata: %#v", rootListing)
	}
	worktreeListing := list("worktree")
	if !worktreeListing.Filtered || directoryHasName(worktreeListing, ".git") {
		t.Fatalf("linked-worktree listing exposed .git pointer: %#v", worktreeListing)
	}
}

func TestFileHandlersTreatBareRepositoryMetadataAsSensitive(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "objects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config"), []byte("[remote \"origin\"]\n\turl = https://credential@example.invalid/private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(nil, NewFileHandlers(scope)...)
	read := registry.Execute(t.Context(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name:      "read_text_file",
			Arguments: `{"path":"config"}`,
		},
	})
	if read.Status != StatusDenied || read.Code != CodeSensitivePath {
		t.Fatalf("bare Git config was not denied: %s", read.JSON())
	}
	listed := registry.Execute(t.Context(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name:      "list_directory",
			Arguments: `{"path":"."}`,
		},
	})
	if listed.Status != StatusCompleted {
		t.Fatalf("bare repository root listing failed: %s", listed.JSON())
	}
	listing, ok := listed.Data.(directoryResult)
	if !ok || !listing.Filtered || len(listing.Entries) != 0 {
		t.Fatalf("bare repository metadata was exposed by listing: %#v", listed.Data)
	}
}

func directoryHasName(result directoryResult, name string) bool {
	for _, entry := range result.Entries {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func TestFileHandlersHonorAdditionalScopeAndReadLimits(t *testing.T) {
	root := t.TempDir()
	additional := t.TempDir()
	allowedFile := filepath.Join(additional, "allowed.txt")
	if err := os.WriteFile(allowedFile, []byte("allowed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary"), []byte{'a', 0, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 5; index++ {
		name := filepath.Join(root, "chunk"+string(rune('a'+index)))
		if err := os.WriteFile(name, []byte(strings.Repeat("x", MaxReadBytes)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scope, err := NewScope(root, []string{additional})
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(nil, NewFileHandlers(scope)...)
	read := func(path string) Result {
		return registry.Execute(context.Background(), model.ToolCall{
			Type: "function",
			Function: model.FunctionCall{
				Name:      "read_text_file",
				Arguments: `{"path":` + quoted(path) + `}`,
			},
		})
	}
	if result := read(allowedFile); result.Status != StatusCompleted || !strings.Contains(result.JSON(), `"content":"allowed"`) {
		t.Fatalf("additional scope was not readable: %s", result.JSON())
	}
	if result := read("binary"); result.Status != StatusFailed || !strings.Contains(result.Message, "binary") {
		t.Fatalf("binary file was not rejected: %s", result.JSON())
	}
	for index := 0; index < 3; index++ {
		if result := read("chunk" + string(rune('a'+index))); result.Status != StatusCompleted {
			t.Fatalf("unexpected read budget failure: %s", result.JSON())
		}
	}
	// The allowed file consumed seven bytes, so the fourth chunk is truncated
	// to the exact remaining aggregate budget.
	if result := read("chunkd"); result.Status != StatusCompleted || !strings.Contains(result.JSON(), `"truncated":true`) {
		t.Fatalf("expected final partial read: %s", result.JSON())
	}
	if result := read("chunke"); result.Status != StatusFailed || !strings.Contains(result.Message, "budget") {
		t.Fatalf("expected aggregate read budget exhaustion: %s", result.JSON())
	}
}

func TestReadTextPrefixBoundsPseudoFileReads(t *testing.T) {
	reader := &repeatingReader{}
	data, truncated, err := readTextPrefix(reader, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(data) != 32 || reader.bytesRead != 33 {
		t.Fatalf("unexpected bounded read: bytes=%d source=%d truncated=%v", len(data), reader.bytesRead, truncated)
	}

	utf8Data, truncated, err := readTextPrefix(strings.NewReader(strings.Repeat("a", 31)+"界tail"), 32)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(utf8Data) != 31 || !strings.HasSuffix(string(utf8Data), "a") {
		t.Fatalf("UTF-8 boundary was not preserved: %q truncated=%v", utf8Data, truncated)
	}
}

type repeatingReader struct {
	bytesRead int
}

func (r *repeatingReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 'x'
	}
	r.bytesRead += len(buffer)
	return len(buffer), nil
}

var _ io.Reader = (*repeatingReader)(nil)

func quoted(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\', '"':
			builder.WriteByte('\\')
			builder.WriteRune(r)
		default:
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
	return builder.String()
}

type testHandler struct {
	result Result
	runs   int
}

func (h *testHandler) Definition() model.ToolDefinition {
	return model.ToolDefinition{Type: "function", Function: model.FunctionDefinition{Name: "test"}}
}

func (h *testHandler) Prepare(context.Context, model.ToolCall) (Invocation, Result) {
	return InvocationFunc(func(context.Context) Result {
		h.runs++
		return h.result
	}), Result{}
}
