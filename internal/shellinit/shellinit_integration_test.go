package shellinit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestZshRegistersWidgetsAndHonorsBindingModes(t *testing.T) {
	zsh := requireShell(t, "zsh")
	tests := []struct {
		name         string
		mode         BindMode
		wantGenerate string
		wantAgent    string
	}{
		{name: "default preserves existing binding", mode: BindDefault, wantGenerate: "self-insert", wantAgent: "self-insert"},
		{name: "no bind preserves existing binding", mode: BindNone, wantGenerate: "self-insert", wantAgent: "self-insert"},
		{name: "force replaces existing binding", mode: BindForce, wantGenerate: "tmh-generate-widget", wantAgent: "tmh-agent-widget"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initPath := writeRenderedScript(t, "zsh", test.mode)
			script := `
bindkey -e
bindkey -M emacs '^X^G' self-insert
bindkey -M emacs '^X^A' self-insert
source "$TMH_INIT"
(( $+widgets[tmh-generate-widget] ))
(( $+widgets[tmh-agent-widget] ))
generate_binding="$(bindkey -M emacs '^X^G')"
agent_binding="$(bindkey -M emacs '^X^A')"
[[ "$generate_binding" == *"$TMH_WANT_GENERATE_BINDING"* ]]
[[ "$agent_binding" == *"$TMH_WANT_AGENT_BINDING"* ]]
`
			runShell(t, zsh, []string{"-fic", script}, map[string]string{
				"TMH_INIT":                  initPath,
				"TMH_WANT_GENERATE_BINDING": test.wantGenerate,
				"TMH_WANT_AGENT_BINDING":    test.wantAgent,
			})
		})
	}
}

func TestZshWidgetPassesOneRequestArgumentAndRestoresFailures(t *testing.T) {
	zsh := requireShell(t, "zsh")
	testWidgetBehavior(t, "zsh", zsh, []string{"-fic"}, `
source "$TMH_INIT"
function zle { :; }
unset REPLY
BUFFER="$TMH_TEST_INPUT"
CURSOR=$TMH_TEST_CURSOR
if [[ "$TMH_WIDGET_MODE" == agent ]]; then
  _tmh_agent_widget
else
  _tmh_generate_widget
fi
widget_status=$?
[[ "$widget_status" == "$TMH_EXPECT_STATUS" ]]
[[ "$BUFFER" == "$TMH_EXPECT_BUFFER" ]]
[[ "$CURSOR" == "$TMH_EXPECT_CURSOR" ]]
(( ! $+parameters[REPLY] ))
`)
}

func TestZshFunctionQueuesGeneratedCommand(t *testing.T) {
	zsh := requireShell(t, "zsh")
	initPath := writeRenderedScript(t, "zsh", BindNone)
	binDir, logPath := writeFakeTMH(t)
	runShell(t, zsh, []string{"-fc", `
source "$TMH_INIT"
tmh "$TMH_TEST_INPUT"
read -z queued
[[ "$queued" == 'echo generated' ]]
`}, map[string]string{
		"PATH":           binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMH_INIT":       initPath,
		"TMH_TEST_INPUT": "request with spaces",
		"TMH_TEST_LOG":   logPath,
	})
	assertLoggedArgument(t, logPath, 3, "request with spaces")
}

func TestBashWidgetPassesOneRequestArgumentAndRestoresFailures(t *testing.T) {
	bash := requireShell(t, "bash")
	testWidgetBehavior(t, "bash", bash, []string{"--noprofile", "--norc", "-c"}, `
source "$TMH_INIT"
unset TMH_WIDGET_QUERY
READLINE_LINE="$TMH_TEST_INPUT"
READLINE_POINT=$TMH_TEST_CURSOR
if [[ "$TMH_WIDGET_MODE" == agent ]]; then
  _tmh_agent_widget
else
  _tmh_generate_widget
fi
widget_status=$?
[[ "$widget_status" == "$TMH_EXPECT_STATUS" ]]
[[ "$READLINE_LINE" == "$TMH_EXPECT_BUFFER" ]]
[[ "$READLINE_POINT" == "$TMH_EXPECT_CURSOR" ]]
[[ -z "${TMH_WIDGET_QUERY+x}" ]]
`)
}

func TestBashBindingAndBash32Fallback(t *testing.T) {
	bash := requireShell(t, "bash")
	output := runShell(t, bash, []string{"--noprofile", "--norc", "-c", `printf '%s' "${BASH_VERSINFO[0]}"`}, nil)
	major, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		t.Fatalf("parse Bash major version %q: %v", output, err)
	}
	if major < 4 {
		defaultInit := writeRenderedScript(t, "bash", BindDefault)
		output = runShell(t, bash, []string{"--noprofile", "--norc", "-ic", `source "$TMH_INIT"`}, map[string]string{"TMH_INIT": defaultInit})
		if !strings.Contains(output, "Bash 3.2 supports command generation but not readline buffer widgets") {
			t.Fatalf("Bash 3.2 fallback notice missing from %q", output)
		}
		noneInit := writeRenderedScript(t, "bash", BindNone)
		output = runShell(t, bash, []string{"--noprofile", "--norc", "-ic", `source "$TMH_INIT"`}, map[string]string{"TMH_INIT": noneInit})
		if strings.Contains(output, "readline buffer widgets") {
			t.Fatalf("--no-bind unexpectedly printed the Bash 3.2 widget notice: %q", output)
		}
		return
	}

	tests := []struct {
		name         string
		mode         BindMode
		wantGenerate string
		wantAgent    string
	}{
		{name: "default", mode: BindDefault, wantGenerate: "existing-generate-widget", wantAgent: "existing-agent-widget"},
		{name: "no-bind", mode: BindNone, wantGenerate: "existing-generate-widget", wantAgent: "existing-agent-widget"},
		{name: "force", mode: BindForce, wantGenerate: "_tmh_generate_widget", wantAgent: "_tmh_agent_widget"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initPath := writeRenderedScript(t, "bash", test.mode)
			script := `
bind -m emacs-standard -x '"\C-x\C-g":existing-generate-widget'
bind -m emacs-standard -x '"\C-x\C-a":existing-agent-widget'
source "$TMH_INIT"
declare -F _tmh_generate_widget >/dev/null
declare -F _tmh_agent_widget >/dev/null
bindings="$(bind -m emacs-standard -X)"
grep -Fq "$TMH_WANT_GENERATE_BINDING" <<<"$bindings"
grep -Fq "$TMH_WANT_AGENT_BINDING" <<<"$bindings"
`
			runShell(t, bash, []string{"--noprofile", "--norc", "-ic", script}, map[string]string{
				"TMH_INIT":                  initPath,
				"TMH_WANT_GENERATE_BINDING": test.wantGenerate,
				"TMH_WANT_AGENT_BINDING":    test.wantAgent,
			})
		})
	}
}

func TestBashWidgetUsesReadlineByteCursor(t *testing.T) {
	bash := requireShell(t, "bash")
	output := runShell(t, bash, []string{"--noprofile", "--norc", "-c", `printf '%s' "${BASH_VERSINFO[0]}"`}, nil)
	major, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		t.Fatalf("parse Bash major version %q: %v", output, err)
	}
	if major < 4 {
		t.Skip("Bash readline widgets require Bash 4 or newer")
	}
	initPath := writeRenderedScript(t, "bash", BindNone)
	binDir, _ := writeFakeTMH(t)
	generated := "echo 你好🙂"
	runShell(t, bash, []string{"--noprofile", "--norc", "-c", `
source "$TMH_INIT"
READLINE_LINE=request
READLINE_POINT=7
_tmh_generate_widget
[[ "$READLINE_LINE" == "$TMH_EXPECT_BUFFER" ]]
[[ "$READLINE_POINT" == "$TMH_EXPECT_CURSOR" ]]
`}, map[string]string{
		"PATH":              binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMH_INIT":          initPath,
		"TMH_TEST_LOG":      filepath.Join(binDir, "unicode-invocation"),
		"TMH_TEST_MODE":     "unicode",
		"TMH_EXPECT_BUFFER": generated,
		"TMH_EXPECT_CURSOR": strconv.Itoa(len(generated)),
	})
}

func TestFishWidgetPassesOneRequestArgumentAndRestoresFailures(t *testing.T) {
	fish := requireShell(t, "fish")
	testWidgetBehavior(t, "fish", fish, []string{"--no-config", "-c"}, `
source "$TMH_INIT"
set -g __tmh_test_buffer "$TMH_TEST_INPUT"
set -g __tmh_test_cursor $TMH_TEST_CURSOR
function commandline
    if test (count $argv) -eq 0
        printf '%s' "$__tmh_test_buffer"
        return
    end
    switch "$argv[1]"
        case -C
            if test (count $argv) -eq 1
                printf '%s\n' "$__tmh_test_cursor"
            else
                set -g __tmh_test_cursor $argv[2]
            end
        case -r
            set -g __tmh_test_buffer $argv[-1]
        case -f
            return 0
    end
end
if test "$TMH_WIDGET_MODE" = agent
    __tmh_agent_widget
else
    __tmh_generate_widget
end
set -l widget_status $status
test "$widget_status" = "$TMH_EXPECT_STATUS"
and test "$__tmh_test_buffer" = "$TMH_EXPECT_BUFFER"
and test "$__tmh_test_cursor" = "$TMH_EXPECT_CURSOR"
and not set -q __tmh_widget_query
`)
}

func TestFishBindingModes(t *testing.T) {
	fish := requireShell(t, "fish")
	tests := []struct {
		name         string
		mode         BindMode
		wantGenerate string
		wantAgent    string
	}{
		{name: "default", mode: BindDefault, wantGenerate: "self-insert", wantAgent: "self-insert"},
		{name: "no-bind", mode: BindNone, wantGenerate: "self-insert", wantAgent: "self-insert"},
		{name: "force", mode: BindForce, wantGenerate: "__tmh_generate_widget", wantAgent: "__tmh_agent_widget"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initPath := writeRenderedScript(t, "fish", test.mode)
			script := `
bind -M default \cx\cg self-insert
bind -M default \cx\ca self-insert
source "$TMH_INIT"
functions -q __tmh_generate_widget
and functions -q __tmh_agent_widget
and bind -M default \cx\cg | string match -q "*$TMH_WANT_GENERATE_BINDING*"
and bind -M default \cx\ca | string match -q "*$TMH_WANT_AGENT_BINDING*"
`
			runShell(t, fish, []string{"--no-config", "--interactive", "-c", script}, map[string]string{
				"TERM":                      "xterm-256color",
				"TMH_INIT":                  initPath,
				"TMH_WANT_GENERATE_BINDING": test.wantGenerate,
				"TMH_WANT_AGENT_BINDING":    test.wantAgent,
			})
		})
	}
}

func testWidgetBehavior(t *testing.T, shellName, executable string, args []string, script string) {
	t.Helper()
	initPath := writeRenderedScript(t, shellName, BindNone)
	binDir, logPath := writeFakeTMH(t)
	generateInput := "  tmh generate first line\nsecond line  "

	for _, success := range []struct {
		name      string
		widget    string
		input     string
		argCount  int
		wantQuery string
	}{
		{name: "generate success", widget: "generate", input: generateInput, argCount: 4, wantQuery: "first line\nsecond line"},
		{name: "agent success", widget: "agent", input: "  tmh agent inspect first line\nsecond line  ", argCount: 5, wantQuery: "inspect first line\nsecond line"},
		{name: "generate leading flag", widget: "generate", input: "  --help me find files  ", argCount: 4, wantQuery: "--help me find files"},
		{name: "agent leading flag", widget: "agent", input: "  --exec=inspection as literal request  ", argCount: 5, wantQuery: "--exec=inspection as literal request"},
	} {
		t.Run(success.name, func(t *testing.T) {
			runShell(t, executable, append(args, script), map[string]string{
				"PATH":              binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMH_INIT":          initPath,
				"TMH_TEST_INPUT":    success.input,
				"TMH_TEST_CURSOR":   "7",
				"TMH_TEST_MODE":     "success",
				"TMH_TEST_LOG":      logPath,
				"TMH_WIDGET_MODE":   success.widget,
				"TMH_EXPECT_STATUS": "0",
				"TMH_EXPECT_BUFFER": "echo generated",
				"TMH_EXPECT_CURSOR": "14",
			})
			assertLoggedArgumentAt(t, logPath, success.argCount-1, "--")
			assertLoggedArgument(t, logPath, success.argCount, success.wantQuery)
		})
	}

	for _, failure := range []struct {
		name       string
		mode       string
		wantStatus string
	}{
		{name: "command failure", mode: "fail", wantStatus: "7"},
		{name: "empty output", mode: "empty", wantStatus: "1"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			runShell(t, executable, append(args, script), map[string]string{
				"PATH":              binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMH_INIT":          initPath,
				"TMH_TEST_INPUT":    generateInput,
				"TMH_TEST_CURSOR":   "7",
				"TMH_TEST_MODE":     failure.mode,
				"TMH_TEST_LOG":      logPath,
				"TMH_WIDGET_MODE":   "generate",
				"TMH_EXPECT_STATUS": failure.wantStatus,
				"TMH_EXPECT_BUFFER": generateInput,
				"TMH_EXPECT_CURSOR": "7",
			})
		})
	}
}

func requireShell(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	return path
}

func writeRenderedScript(t *testing.T, shell string, mode BindMode) string {
	t.Helper()
	script, err := Render(shell, mode)
	if err != nil {
		t.Fatalf("render %s init: %v", shell, err)
	}
	path := filepath.Join(t.TempDir(), "tmh."+shell)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("write rendered %s init: %v", shell, err)
	}
	return path
}

func writeFakeTMH(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tmh")
	script := `#!/bin/sh
set -eu
: "${TMH_TEST_LOG:?}"
printf '%s\n' "$#" > "$TMH_TEST_LOG.count"
index=0
for argument do
  index=$((index + 1))
  printf '%s' "$argument" > "$TMH_TEST_LOG.$index"
done
case "${TMH_TEST_MODE:-success}" in
  success) printf '%s\n' 'echo generated' ;;
  unicode) printf '%s\n' 'echo 你好🙂' ;;
  empty) exit 0 ;;
  fail) exit 7 ;;
  *) exit 99 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake tmh: %v", err)
	}
	return dir, filepath.Join(dir, "invocation")
}

func assertLoggedArgument(t *testing.T, logPath string, count int, want string) {
	t.Helper()
	countData, err := os.ReadFile(logPath + ".count")
	if err != nil {
		t.Fatalf("read fake tmh argument count: %v", err)
	}
	if got := strings.TrimSpace(string(countData)); got != strconv.Itoa(count) {
		t.Fatalf("fake tmh argument count = %s, want %d", got, count)
	}
	assertLoggedArgumentAt(t, logPath, count, want)
}

func assertLoggedArgumentAt(t *testing.T, logPath string, index int, want string) {
	t.Helper()
	argument, err := os.ReadFile(fmt.Sprintf("%s.%d", logPath, index))
	if err != nil {
		t.Fatalf("read fake tmh argument %d: %v", index, err)
	}
	if got := string(argument); got != want {
		t.Fatalf("fake tmh argument %d = %q, want %q", index, got, want)
	}
}

func runShell(t *testing.T, executable string, args []string, environment map[string]string) string {
	t.Helper()
	command := exec.Command(executable, args...)
	command.Env = mergedEnvironment(environment)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %q: %v\n%s", executable, args, err, output)
	}
	return string(output)
}

func mergedEnvironment(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[name]; !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range overrides {
		result = append(result, name+"="+value)
	}
	return result
}
