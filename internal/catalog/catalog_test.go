package catalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/codearts"
	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestFallbackModelsWithoutAccounts(t *testing.T) {
	settings := config.Load()
	settings.AccountsDir = filepath.Join(t.TempDir(), "accounts")
	settings.DefaultModel = "GLM-5.2"
	settings.Models = []string{"GLM-5.2", "Other-Model"}
	manager := auth.NewManager(settings)
	client := codearts.New(settings, manager)
	catalog := New(settings, manager, client)

	models, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "GLM-5.2" || models[1].ID != "Other-Model" {
		t.Fatalf("unexpected fallback models: %#v", models)
	}
	if models[0].Sources[0] != "fallback" {
		t.Fatalf("expected fallback source: %#v", models[0])
	}
}
