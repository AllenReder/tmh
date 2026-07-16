package command

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxExplanationBytes = 2 * 1024

type Result struct {
	Command     string `json:"command"`
	Explanation string `json:"explanation"`
}

func ParseResult(content string) (Result, error) {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(content)))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("response is not the required JSON object: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Result{}, fmt.Errorf("response contains trailing content")
		}
		return Result{}, fmt.Errorf("decode trailing response content: %w", err)
	}
	result.Command = strings.TrimSpace(result.Command)
	if !utf8.ValidString(result.Explanation) {
		return Result{}, fmt.Errorf("explanation is not valid UTF-8")
	}
	for _, r := range result.Explanation {
		if unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) || (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') {
			return Result{}, fmt.Errorf("explanation contains a control or formatting character")
		}
	}
	result.Explanation = strings.Join(strings.Fields(result.Explanation), " ")
	if result.Explanation == "" {
		return Result{}, fmt.Errorf("explanation is empty")
	}
	if len(result.Explanation) > MaxExplanationBytes {
		return Result{}, fmt.Errorf("explanation exceeds %d bytes", MaxExplanationBytes)
	}
	return result, nil
}
