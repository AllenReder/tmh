package generator

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
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
	if err := ensureEOF(decoder); err != nil {
		return Result{}, err
	}
	result.Command = strings.TrimSpace(result.Command)
	result.Explanation = strings.Join(strings.Fields(result.Explanation), " ")
	if result.Explanation == "" {
		return Result{}, fmt.Errorf("explanation is empty")
	}
	if len(result.Explanation) > MaxExplanationBytes {
		return Result{}, fmt.Errorf("explanation exceeds %d bytes", MaxExplanationBytes)
	}
	return result, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing response content: %w", err)
	}
	return fmt.Errorf("response contains trailing content")
}
