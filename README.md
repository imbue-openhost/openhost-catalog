# openhost-catalog

Go + HTML template app for OpenHost app discovery and one-click publishing.

## What it does

- Aggregates app entries from a configurable list of JSON feed sources
- Renders a server-side catalog UI (no React)
- Publishes apps to OpenHost with a single click
- Polls deployment status and app logs
- Falls back to OpenHost's native installer flow when router token access is not configured

Deployment configuration is always read from the target repo's `openhost.toml` during deploy.

## Feed format

Each source URL must return JSON with schema `openhost.catalog.v1`:

```json
{
  "schema": "openhost.catalog.v1",
  "source_id": "official",
  "source_name": "OpenHost Official",
  "generated_at": "2026-03-28T00:00:00Z",
  "apps": [
    {
      "name": "searxng",
      "title": "SearXNG",
      "description": "Privacy-respecting metasearch engine",
      "repo_url": "https://github.com/imbue-openhost/openhost-searxng",
      "repo_ref": "",
      "icon_url": "",
      "tags": ["search", "privacy"],
      "categories": ["search"],
      "website_url": "https://docs.searxng.org",
      "docs_url": "https://github.com/imbue-openhost/openhost-searxng#readme"
    }
  ]
}
```

Required fields: `name`, `title`, `repo_url`. All others may be omitted.

`name` must be lowercase alphanumeric with optional interior hyphens (matches OpenHost's app name format). It is the catalog's identifier for the app, the default name when deploying, and must be unique within a source.

## Runtime configuration

- `LISTEN_ADDR` (default `:8080`)
- `OPENHOST_SQLITE_main` (preferred DB path from OpenHost)
- `CATALOG_DB_PATH` (DB path fallback)
- `OPENHOST_ROUTER_URL` (default `http://host.docker.internal:8080`)
- `APP_REPO_ROUTER_TOKEN` (optional direct router API token for one-click publish)
- `OPENHOST_APP_TOKEN` (used to fetch `APP_REPO_ROUTER_TOKEN` from secrets service)
- `OPENHOST_APP_NAME` (default `openhost-catalog`)
- `OPENHOST_APP_BASE_PATH` (injected by OpenHost; used for path-based routing compatibility)
- `DEFAULT_SOURCE_URL` (auto-seeded on first boot if no sources are configured; defaults to the official `imbue-openhost/openhost-apps` catalog)
- `CATALOG_ALLOW_HTTP_REPO_URLS` (default `false`)
- `CATALOG_ALLOW_FILE_URLS` (default `false`)
- `CATALOG_HTTP_TIMEOUT_SECONDS` (default `10`)

## Development

```bash
go mod tidy
go run ./cmd/openhost-catalog
```

Open `http://localhost:8080`.

## OpenHost manifest

See `openhost.toml`.
