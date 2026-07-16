package openai

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AllenReder/tmh/internal/model"
)

func TestLiveEndpoint(t *testing.T) {
	if os.Getenv("TMH_LIVE_TEST") != "1" {
		t.Skip("set TMH_LIVE_TEST=1 to run the opt-in live endpoint test")
	}
	baseURL := os.Getenv("TMH_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	modelName := os.Getenv("TMH_MODEL")
	if modelName == "" {
		t.Fatal("TMH_MODEL is required for the live test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &Client{BaseURL: baseURL, APIKey: os.Getenv("TMH_API_KEY")}
	if _, err := client.Complete(ctx, model.Request{Model: modelName, Messages: []model.Message{{Role: model.RoleUser, Content: "Reply with OK."}}}); err != nil {
		t.Fatal(err)
	}
}
