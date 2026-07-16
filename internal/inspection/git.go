package inspection

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/AllenReder/tmh/internal/tool"
)

func planGit(arguments []string, cwd string, scope *tool.Scope) ([]string, map[string]string, [][]string, error) {
	if len(arguments) == 0 {
		return nil, nil, nil, fmt.Errorf("git subcommand is required")
	}
	subcommand := arguments[0]
	allowed := map[string]func([]string) error{
		"status":    validateGitStatus,
		"diff":      validateGitDiff,
		"log":       validateGitLog,
		"show":      validateGitShow,
		"rev-parse": validateGitRevParse,
		"ls-files":  validateGitLSFiles,
	}
	validator, ok := allowed[subcommand]
	if !ok {
		return nil, nil, nil, fmt.Errorf("git subcommand %q is not allowed", subcommand)
	}
	for _, argument := range arguments[1:] {
		if strings.IndexByte(argument, 0) >= 0 {
			return nil, nil, nil, fmt.Errorf("git arguments cannot contain NUL")
		}
		if tool.IsSensitiveArgument(argument) {
			return nil, nil, nil, fmt.Errorf("git argument targets sensitive data")
		}
	}
	if err := validator(arguments[1:]); err != nil {
		return nil, nil, nil, err
	}
	if err := validateLocalGitConfig(cwd, scope); err != nil {
		return nil, nil, nil, fmt.Errorf("unsafe repository configuration: %w", err)
	}
	preflights, err := gitCommitPreflights(subcommand, arguments[1:])
	if err != nil {
		return nil, nil, nil, err
	}

	planned := []string{"--no-pager", "--no-optional-locks", "--no-replace-objects", subcommand}
	if subcommand == "diff" || subcommand == "log" || subcommand == "show" {
		planned = append(planned, "--no-ext-diff", "--no-textconv")
	}
	if subcommand == "status" || subcommand == "diff" || subcommand == "log" || subcommand == "show" {
		planned = append(planned, "--ignore-submodules=all")
	}
	planned = append(planned, arguments[1:]...)
	if subcommand != "rev-parse" {
		if !containsArgument(arguments[1:], "--") {
			planned = append(planned, "--")
		}
		for _, pattern := range sensitiveGitPathspecs() {
			planned = append(planned, pattern)
		}
	}

	config := []struct{ key, value string }{
		{"core.fsmonitor", "false"},
		{"core.untrackedCache", "false"},
		{"core.hooksPath", "/dev/null"},
		{"core.pager", "cat"},
		{"pager.status", "false"},
		{"pager.diff", "false"},
		{"pager.log", "false"},
		{"pager.show", "false"},
		{"diff.external", ""},
		{"diff.trustExitCode", "false"},
		{"interactive.diffFilter", ""},
		{"core.attributesFile", "/dev/null"},
		{"mailmap.file", "/dev/null"},
		{"mailmap.blob", ""},
		{"gpg.program", "/bin/false"},
		{"gpg.ssh.program", "/bin/false"},
		{"core.sshCommand", "/bin/false"},
		{"format.pretty", "medium"},
		{"log.showSignature", "false"},
		{"diff.orderFile", "/dev/null"},
		{"diff.ignoreSubmodules", "all"},
		{"status.submoduleSummary", "false"},
		{"color.ui", "false"},
		{"credential.helper", ""},
		{"protocol.allow", "never"},
		{"submodule.recurse", "false"},
	}
	environment := map[string]string{
		"GIT_CONFIG_NOSYSTEM":    "1",
		"GIT_CONFIG_SYSTEM":      "/dev/null",
		"GIT_CONFIG_GLOBAL":      "/dev/null",
		"GIT_OPTIONAL_LOCKS":     "0",
		"GIT_TERMINAL_PROMPT":    "0",
		"GIT_ASKPASS":            "/bin/false",
		"SSH_ASKPASS":            "/bin/false",
		"GCM_INTERACTIVE":        "Never",
		"GIT_PAGER":              "cat",
		"PAGER":                  "cat",
		"GIT_NO_LAZY_FETCH":      "1",
		"GIT_NO_REPLACE_OBJECTS": "1",
		"NO_COLOR":               "1",
		"GIT_CONFIG_COUNT":       fmt.Sprintf("%d", len(config)),
	}
	for index, item := range config {
		environment[fmt.Sprintf("GIT_CONFIG_KEY_%d", index)] = item.key
		environment[fmt.Sprintf("GIT_CONFIG_VALUE_%d", index)] = item.value
	}
	if runtime.GOOS == "darwin" {
		for _, developerDirectory := range []string{"/Library/Developer/CommandLineTools", "/Applications/Xcode.app/Contents/Developer"} {
			if info, err := os.Stat(developerDirectory); err == nil && info.IsDir() {
				environment["DEVELOPER_DIR"] = developerDirectory
				break
			}
		}
	}
	return planned, environment, preflights, nil
}

func gitCommitPreflights(subcommand string, arguments []string) ([][]string, error) {
	if subcommand != "show" && subcommand != "diff" {
		return nil, nil
	}
	positionals := make([]string, 0, 2)
	for _, argument := range arguments {
		if argument == "--" {
			break
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			positionals = append(positionals, argument)
		}
	}
	if subcommand == "show" && len(positionals) == 0 {
		positionals = append(positionals, "HEAD")
	}
	if subcommand == "diff" && len(positionals) > 2 {
		return nil, fmt.Errorf("git diff accepts at most two inspected commit revisions; put file paths after --")
	}
	revisions := make([]string, 0, len(positionals)*2)
	for _, positional := range positionals {
		expanded, err := expandCommitRange(positional)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, expanded...)
	}
	preflights := make([][]string, 0, len(revisions))
	for _, revision := range revisions {
		preflights = append(preflights, []string{
			"--no-pager", "--no-optional-locks", "--no-replace-objects",
			"rev-parse", "--verify", "--quiet", revision + "^{commit}",
		})
	}
	return preflights, nil
}

func expandCommitRange(value string) ([]string, error) {
	separator := ""
	if strings.Contains(value, "...") {
		separator = "..."
	} else if strings.Contains(value, "..") {
		separator = ".."
	}
	values := []string{value}
	if separator != "" {
		if strings.Count(value, separator) != 1 {
			return nil, fmt.Errorf("Git revision range is ambiguous")
		}
		values = strings.SplitN(value, separator, 2)
		for index := range values {
			if values[index] == "" {
				values[index] = "HEAD"
			}
		}
	}
	for _, revision := range values {
		if !validSimpleCommitish(revision) {
			return nil, fmt.Errorf("Git revision %q is not an allowed commit-ish expression", revision)
		}
	}
	return values, nil
}

func validSimpleCommitish(value string) bool {
	if value == "" || len(value) > 256 || value[0] == '.' || value[0] == '/' || value[0] == '-' || strings.Contains(value, "..") || strings.Contains(value, "//") {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._/-~^", r) {
			continue
		}
		return false
	}
	return true
}

func sensitiveGitPathspecs() []string {
	return []string{
		":(exclude,icase,glob)**/.env",
		":(exclude,icase,glob)**/.env.*",
		":(exclude,icase,glob)**/.envrc",
		":(exclude,icase,glob)**/.gitmodules",
		":(exclude,icase,glob)**/.ssh/**",
		":(exclude,icase,glob)**/.aws/**",
		":(exclude,icase,glob)**/.azure/**",
		":(exclude,icase,glob)**/.docker/**",
		":(exclude,icase,glob)**/.config/gcloud/**",
		":(exclude,icase,glob)**/.gnupg/**",
		":(exclude,icase,glob)**/.kube/**",
		":(exclude,icase,glob)**/.terraform.d/**",
		":(exclude,icase,glob)**/.netrc",
		":(exclude,icase,glob)**/.git-credentials",
		":(exclude,icase,glob)**/.npmrc",
		":(exclude,icase,glob)**/.pypirc",
		":(exclude,icase,glob)**/.vault-token",
		":(exclude,icase,glob)**/*secret*",
		":(exclude,icase,glob)**/*credential*",
		":(exclude,icase,glob)**/*password*",
		":(exclude,icase,glob)**/*token*",
		":(exclude,icase,glob)**/*.pem",
		":(exclude,icase,glob)**/*.key",
		":(exclude,icase,glob)**/*.p12",
		":(exclude,icase,glob)**/*.pfx",
	}
}

func containsArgument(arguments []string, target string) bool {
	for _, argument := range arguments {
		if argument == target {
			return true
		}
	}
	return false
}

func validateGitStatus(args []string) error {
	exact := stringSet(
		"--short", "-s", "--porcelain", "--branch", "-b", "--show-stash", "--ahead-behind", "--no-ahead-behind",
		"--renames", "--no-renames", "--find-renames", "--no-column", "--untracked-files", "--ignored", "-uno", "-unormal", "-uall",
	)
	attached := map[string]func(string) bool{
		"--porcelain=":       oneOf("v1", "v2"),
		"--untracked-files=": oneOf("no", "normal", "all"),
		"--ignored=":         oneOf("no", "traditional", "matching"),
		"--find-renames=":    validSimilarity,
	}
	return validateGitOptions("status", args, exact, attached)
}

func validateGitDiff(args []string) error {
	exact := stringSet(
		"--cached", "--staged", "--stat", "--numstat", "--shortstat", "--name-only", "--name-status",
		"--check", "--summary", "--patch", "-p", "--no-patch", "-s", "--raw", "--minimal",
		"--patience", "--histogram", "--word-diff", "--word-diff=plain", "--relative", "--no-renames", "--find-renames",
	)
	attached := map[string]func(string) bool{
		"--unified=":      positiveDecimal,
		"-U":              positiveDecimal,
		"--diff-filter=":  validDiffFilter,
		"--find-renames=": validSimilarity,
		"--stat=":         validStatDimensions,
	}
	return validateGitOptions("diff", args, exact, attached)
}

func validateGitLog(args []string) error {
	exact := stringSet(
		"--oneline", "--stat", "--shortstat", "--name-only", "--name-status", "--decorate", "--no-decorate",
		"--all", "--branches", "--tags", "--remotes", "--first-parent", "--merges", "--no-merges", "--reverse",
		"--patch", "-p", "--no-patch", "-s", "--date-order", "--topo-order",
	)
	attached := map[string]func(string) bool{
		"--format=":    validPrettyFormat,
		"--pretty=":    validPretty,
		"--max-count=": positiveDecimal,
		"-n":           positiveDecimal,
		"--since=":     boundedText,
		"--until=":     boundedText,
		"--author=":    boundedText,
		"--grep=":      boundedText,
		"--date=":      boundedText,
	}
	return validateGitOptions("log", args, exact, attached)
}

func validateGitShow(args []string) error {
	exact := stringSet("--stat", "--shortstat", "--name-only", "--name-status", "--oneline", "--patch", "-p", "--no-patch", "-s")
	attached := map[string]func(string) bool{
		"--format=": validPrettyFormat,
		"--pretty=": validPretty,
		"--date=":   boundedText,
	}
	return validateGitOptions("show", args, exact, attached)
}

func validateGitRevParse(args []string) error {
	exact := stringSet(
		"--show-toplevel", "--show-prefix", "--show-cdup", "--git-dir", "--absolute-git-dir",
		"--is-inside-work-tree", "--is-inside-git-dir", "--is-bare-repository", "--show-superproject-working-tree",
		"--verify", "--quiet", "-q", "--short", "--abbrev-ref", "--symbolic-full-name",
	)
	attached := map[string]func(string) bool{
		"--short=":       positiveDecimal,
		"--path-format=": oneOf("absolute", "relative"),
	}
	return validateGitOptions("rev-parse", args, exact, attached)
}

func validateGitLSFiles(args []string) error {
	exact := stringSet(
		"--cached", "-c", "--modified", "-m", "--deleted", "-d", "--others", "-o", "--ignored", "-i",
		"--exclude-standard", "--stage", "-s", "--unmerged", "-u", "--error-unmatch", "--full-name",
	)
	return validateGitOptions("ls-files", args, exact, map[string]func(string) bool{"--format=": boundedFormat})
}

func validateGitOptions(command string, args []string, exact map[string]struct{}, attached map[string]func(string) bool) error {
	afterSeparator := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if afterSeparator {
			if unsafeRelativePath(argument) {
				return fmt.Errorf("git %s path escapes the working directory", command)
			}
			continue
		}
		if argument == "--" {
			afterSeparator = true
			continue
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			continue
		}
		if _, ok := exact[argument]; ok {
			continue
		}
		matched := false
		for prefix, validate := range attached {
			if strings.HasPrefix(argument, prefix) && len(argument) > len(prefix) {
				matched = validate(strings.TrimPrefix(argument, prefix))
				break
			}
		}
		if matched {
			continue
		}
		return fmt.Errorf("git %s option %q is not allowed", command, argument)
	}
	return nil
}

func oneOf(values ...string) func(string) bool {
	allowed := stringSet(values...)
	return func(value string) bool {
		_, ok := allowed[value]
		return ok
	}
}

func positiveDecimal(value string) bool {
	if value == "" || len(value) > 9 {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number >= 0
}

func validSimilarity(value string) bool {
	value = strings.TrimSuffix(value, "%")
	if !positiveDecimal(value) {
		return false
	}
	number, _ := strconv.Atoi(value)
	return number <= 100
}

func validDiffFilter(value string) bool {
	if value == "" || len(value) > 16 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("ACDMRTUXBacdmrtuxb*", r) {
			return false
		}
	}
	return true
}

func validStatDimensions(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && r != ',' {
			return false
		}
	}
	return true
}

func boundedText(value string) bool {
	return value != "" && len(value) <= 1024 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func boundedFormat(value string) bool {
	return boundedText(value) && len(value) <= 4096
}

func validPretty(value string) bool {
	if _, ok := stringSet("oneline", "short", "medium", "full", "fuller", "reference", "email", "mboxrd", "raw")[value]; ok {
		return true
	}
	for _, prefix := range []string{"format:", "tformat:"} {
		if strings.HasPrefix(value, prefix) {
			return validPrettyFormat(strings.TrimPrefix(value, prefix))
		}
	}
	return false
}

func validPrettyFormat(value string) bool {
	if !boundedFormat(value) {
		return false
	}
	// %G* placeholders request signature verification and can invoke an
	// externally configured GPG/SSH verifier. Inspection never needs it.
	return !strings.Contains(value, "%G")
}

func stringSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func unsafeRelativePath(path string) bool {
	clean := strings.ReplaceAll(path, "\\", "/")
	return clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../")
}
