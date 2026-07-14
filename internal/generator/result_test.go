package generator

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
	} {
		if _, err := ParseResult(input); err == nil {
			t.Fatalf("expected parse error for %q", input)
		}
	}
}
