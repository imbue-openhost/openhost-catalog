package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/imbue-ai/openhost-catalog/internal/store"
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
	ID                     string   `json:"id"`
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	RepoURL                string   `json:"repo_url"`
	RepoRef                string   `json:"repo_ref"`
	DefaultAppName         string   `json:"default_app_name"`
	IconURL                string   `json:"icon_url"`
	Tags                   []string `json:"tags"`
	Categories             []string `json:"categories"`
	WebsiteURL             string   `json:"website_url"`
	DocsURL                string   `json:"docs_url"`
	MinimumOpenHostVersion string   `json:"minimum_openhost_version"`
	Verified               bool     `json:"verified"`
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
	if src.ETag != "" {
		req.Header.Set("If-None-Match", src.ETag)
	}
	if src.LastModified != "" {
		req.Header.Set("If-Modified-Since", src.LastModified)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		_ = s.store.UpdateSourceError(ctx, sourceID, "fetch failed: "+err.Error())
		return fmt.Errorf("fetch source feed: %w", err)
	}
	defer resp.Body.Close()

	etag := resp.Header.Get("ETag")
	if etag == "" {
		etag = src.ETag
	}
	lastModified := resp.Header.Get("Last-Modified")
	if lastModified == "" {
		lastModified = src.LastModified
	}

	if resp.StatusCode == http.StatusNotModified {
		name := src.Name
		if name == "" {
			name = src.ID
		}
		if err := s.store.UpdateSourceAfterSync(ctx, sourceID, name, etag, lastModified, ""); err != nil {
			return err
		}
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		errMsg := strings.TrimSpace(string(body))
		if errMsg == "" {
			errMsg = "unexpected status: " + resp.Status
		}
		_ = s.store.UpdateSourceError(ctx, sourceID, errMsg)
		return fmt.Errorf("source returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		_ = s.store.UpdateSourceError(ctx, sourceID, "read failed: "+err.Error())
		return fmt.Errorf("read source response body: %w", err)
	}

	var feed sourceFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		_ = s.store.UpdateSourceError(ctx, sourceID, "invalid JSON feed")
		return fmt.Errorf("parse source JSON: %w", err)
	}
	if strings.TrimSpace(feed.Schema) != "openhost.catalog.v1" {
		errMsg := "unsupported feed schema"
		_ = s.store.UpdateSourceError(ctx, sourceID, errMsg)
		return fmt.Errorf("%s: %q", errMsg, feed.Schema)
	}

	apps := make([]store.CatalogApp, 0, len(feed.Apps))
	for _, a := range feed.Apps {
		app, ok := normalizeFeedApp(sourceID, a)
		if !ok {
			continue
		}
		apps = append(apps, app)
	}

	if err := s.store.ReplaceCatalogAppsForSource(ctx, sourceID, apps); err != nil {
		_ = s.store.UpdateSourceError(ctx, sourceID, "db update failed: "+err.Error())
		return err
	}

	name := strings.TrimSpace(feed.SourceName)
	if name == "" {
		name = src.Name
	}
	if name == "" {
		name = sourceID
	}

	if err := s.store.UpdateSourceAfterSync(ctx, sourceID, name, etag, lastModified, ""); err != nil {
		return err
	}

	return nil
}

func normalizeFeedApp(sourceID string, in sourceFeedApp) (store.CatalogApp, bool) {
	appID := strings.TrimSpace(in.ID)
	if appID == "" {
		appID = makeSlug(strings.TrimSpace(in.Title))
	}
	if !validIDPattern.MatchString(appID) {
		return store.CatalogApp{}, false
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = appID
	}

	repoURL := strings.TrimSpace(in.RepoURL)
	if repoURL == "" {
		return store.CatalogApp{}, false
	}
	parsed, err := url.Parse(repoURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return store.CatalogApp{}, false
	}

	defaultAppName := strings.TrimSpace(in.DefaultAppName)
	if defaultAppName == "" {
		defaultAppName = appID
	}

	out := store.CatalogApp{
		SourceID:               sourceID,
		AppID:                  appID,
		Title:                  title,
		Description:            strings.TrimSpace(in.Description),
		RepoURL:                repoURL,
		RepoRef:                strings.TrimSpace(in.RepoRef),
		DefaultAppName:         defaultAppName,
		IconURL:                strings.TrimSpace(in.IconURL),
		Tags:                   compactList(in.Tags),
		Categories:             compactList(in.Categories),
		WebsiteURL:             strings.TrimSpace(in.WebsiteURL),
		DocsURL:                strings.TrimSpace(in.DocsURL),
		MinimumOpenHostVersion: strings.TrimSpace(in.MinimumOpenHostVersion),
		Verified:               in.Verified,
	}

	return out, true
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
