package command

import "testing"

func TestParseResult(t *testing.T) {
	result, err := ParseResult("{\"command\":\"echo hello\",\"explanation\":\"Prints\\nhello.\"}")
	if err != nil {
		t.Fatal(err)
	}
	if result.Command != "echo hello" || result.Explanation != "Prints hello." {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestParseResultRejectsExtraFieldsAndTrailingText(t *testing.T) {
	for _, input := range []string{
		`{"command":"echo hi","explanation":"x","extra":true}`,
		`{"command":"echo hi","explanation":"x"} trailing`,
		`{"command":"echo hi","explanation":"\u001b]8;;https://example.invalid\u0007link"}`,
		`{"command":"echo hi","explanation":"safe\u202eevil"}`,
		`{"command":"echo hi","explanation":"safe\u2028evil"}`,
	} {
		if _, err := ParseResult(input); err == nil {
			t.Fatalf("expected parse error for %q", input)
		}
	}
}
