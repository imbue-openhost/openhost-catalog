package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/imbue-openhost/openhost-catalog/internal/store"
)

var validIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

type Service struct {
	store      *store.Store
	httpClient *http.Client
}

type sourceFeed struct {
	Schema     string          `json:"schema"`
	SourceID   string          `json:"source_id"`
	SourceName string          `json:"source_name"`
	Generated  string          `json:"generated_at"`
	Apps       []sourceFeedApp `json:"apps"`
}

type sourceFeedApp struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	RepoURL     string   `json:"repo_url"`
	RepoRef     string   `json:"repo_ref"`
	IconURL     string   `json:"icon_url"`
	Tags        []string `json:"tags"`
	Categories  []string `json:"categories"`
	WebsiteURL  string   `json:"website_url"`
	DocsURL     string   `json:"docs_url"`
}

func NewService(st *store.Store, client *http.Client) *Service {
	return &Service{store: st, httpClient: client}
}

func (s *Service) SyncSource(ctx context.Context, sourceID string) error {
	src, err := s.store.GetSource(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("load source: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return fmt.Errorf("create source request: %w", err)
	}
	// Ask upstream CDNs to revalidate against origin; raw.githubusercontent.com
	// can serve stale JSON for minutes otherwise.
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		_ = s.store.MarkSourceSyncFailed(ctx, sourceID, "fetch failed: "+err.Error())
		return fmt.Errorf("fetch source feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		errMsg := strings.TrimSpace(string(body))
		if errMsg == "" {
			errMsg = "unexpected status: " + resp.Status
		}
		_ = s.store.MarkSourceSyncFailed(ctx, sourceID, errMsg)
		return fmt.Errorf("source returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		_ = s.store.MarkSourceSyncFailed(ctx, sourceID, "read failed: "+err.Error())
		return fmt.Errorf("read source response body: %w", err)
	}

	var feed sourceFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		_ = s.store.MarkSourceSyncFailed(ctx, sourceID, "invalid JSON feed")
		return fmt.Errorf("parse source JSON: %w", err)
	}
	if strings.TrimSpace(feed.Schema) != "openhost.catalog.v1" {
		errMsg := "unsupported feed schema"
		_ = s.store.MarkSourceSyncFailed(ctx, sourceID, errMsg)
		return fmt.Errorf("%s: %q", errMsg, feed.Schema)
	}

	apps := make([]store.CatalogApp, 0, len(feed.Apps))
	seen := make(map[string]struct{}, len(feed.Apps))
	for _, a := range feed.Apps {
		app, ok := normalizeFeedApp(sourceID, a)
		if !ok {
			continue
		}
		// A source may not contain two apps with the same derived ID.
		// (Across sources, collisions are fine; within a source, this is a
		// feed publisher error that needs to be fixed in the source feed.)
		if _, dup := seen[app.AppID]; dup {
			errMsg := fmt.Sprintf("duplicate app id %q in source feed; app IDs must be unique within a source", app.AppID)
			_ = s.store.MarkSourceSyncFailed(ctx, sourceID, errMsg)
			return errors.New(errMsg)
		}
		seen[app.AppID] = struct{}{}
		apps = append(apps, app)
	}

	if err := s.store.ReplaceCatalogAppsForSource(ctx, sourceID, apps); err != nil {
		_ = s.store.MarkSourceSyncFailed(ctx, sourceID, "db update failed: "+err.Error())
		return err
	}

	name := strings.TrimSpace(feed.SourceName)
	if name == "" {
		name = src.Name
	}
	if name == "" {
		name = sourceID
	}

	if err := s.store.MarkSourceSynced(ctx, sourceID, name); err != nil {
		return err
	}

	return nil
}

func normalizeFeedApp(sourceID string, in sourceFeedApp) (store.CatalogApp, bool) {
	repoURL := strings.TrimSpace(in.RepoURL)
	if repoURL == "" {
		return store.CatalogApp{}, false
	}
	parsed, err := url.Parse(repoURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return store.CatalogApp{}, false
	}

	// Derive a stable app ID from the repo URL's last path segment (e.g.
	// "owner/openhost-synapse" -> "openhost-synapse"), falling back to the
	// title slug if the URL doesn't yield a valid ID.
	appID := makeSlug(appIDCandidateFromRepoURL(parsed))
	if !validIDPattern.MatchString(appID) {
		appID = makeSlug(strings.TrimSpace(in.Title))
	}
	if !validIDPattern.MatchString(appID) {
		return store.CatalogApp{}, false
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = appID
	}

	out := store.CatalogApp{
		SourceID:    sourceID,
		AppID:       appID,
		Title:       title,
		Description: strings.TrimSpace(in.Description),
		RepoURL:     repoURL,
		RepoRef:     strings.TrimSpace(in.RepoRef),
		IconURL:     strings.TrimSpace(in.IconURL),
		Tags:        compactList(in.Tags),
		Categories:  compactList(in.Categories),
		WebsiteURL:  strings.TrimSpace(in.WebsiteURL),
		DocsURL:     strings.TrimSpace(in.DocsURL),
	}

	return out, true
}

// appIDCandidateFromRepoURL returns the last non-empty path segment of a
// parsed repo URL, stripping a trailing ".git" suffix if present. Returns an
// empty string if no suitable segment exists.
func appIDCandidateFromRepoURL(u *url.URL) string {
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		return ""
	}
	idx := strings.LastIndex(path, "/")
	last := path
	if idx >= 0 {
		last = path[idx+1:]
	}
	last = strings.TrimSuffix(last, ".git")
	return last
}

func compactList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func makeSlug(in string) string {
	in = strings.ToLower(strings.TrimSpace(in))
	replacer := strings.NewReplacer(
		" ", "-",
		"_", "-",
		"/", "-",
		".", "-",
	)
	in = replacer.Replace(in)
	in = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(in, "")
	in = regexp.MustCompile(`-+`).ReplaceAllString(in, "-")
	in = strings.Trim(in, "-")
	if in == "" {
		return "app"
	}
	return in
}
