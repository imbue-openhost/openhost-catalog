package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// TestIntegrationRoundTrip verifies that an app's integration block
// survives a write/read cycle and that the source's integrations
// vocabulary is stored alongside the sync record.
func TestIntegrationRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "catalog.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := store.CreateSource(ctx, Source{
		ID:      "official",
		Name:    "OpenHost Official",
		URL:     "https://example.invalid/catalog.json",
		Enabled: true,
	}); err != nil {
		t.Fatalf("create source: %v", err)
	}

	vocab := map[string]IntegrationVocabEntry{
		"zone_owner_auto_login": {
			Title:       "Zone-owner auto-login",
			Description: "Owner's zone_auth cookie is verified.",
		},
		"respects_data_dirs": {
			Title:       "Data directories",
			Description: "App uses OPENHOST_APP_DATA_DIR.",
		},
	}

	if err := store.MarkSourceSynced(ctx, "official", "OpenHost Official", vocab); err != nil {
		t.Fatalf("mark synced: %v", err)
	}

	src, err := store.GetSource(ctx, "official")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if !reflect.DeepEqual(src.IntegrationsVocab, vocab) {
		t.Errorf("vocab round-trip mismatch: got %+v want %+v", src.IntegrationsVocab, vocab)
	}

	apps := []CatalogApp{{
		SourceID:    "official",
		AppID:       "searxng",
		Title:       "SearXNG",
		Description: "Privacy-respecting metasearch",
		RepoURL:     "https://example.invalid/searxng",
		Integration: Integration{
			Level:   5,
			Summary: "Stateless public search.",
			Has:     []string{"respects_data_dirs"},
			NotApplicable: []string{
				"zone_owner_auto_login",
			},
		},
	}}

	if err := store.ReplaceCatalogAppsForSource(ctx, "official", apps); err != nil {
		t.Fatalf("replace apps: %v", err)
	}

	got, err := store.GetCatalogApp(ctx, "official", "searxng")
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	want := apps[0].Integration
	want.Missing = nil // Integration.Missing stored as nil round-trips to nil
	if !reflect.DeepEqual(got.Integration, want) {
		t.Errorf("integration round-trip mismatch:\n got: %+v\n want: %+v", got.Integration, want)
	}
}

// TestIntegrationMigrationIdempotent checks that re-running Init
// against an already-migrated database is a no-op rather than
// erroring on the duplicate column ALTERs.
func TestIntegrationMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.db")
	store1, err := Open(path)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	if err := store1.Init(context.Background()); err != nil {
		t.Fatalf("init 1: %v", err)
	}
	store1.Close()

	store2, err := Open(path)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	defer store2.Close()
	if err := store2.Init(context.Background()); err != nil {
		t.Fatalf("init 2 (migration not idempotent): %v", err)
	}
}
