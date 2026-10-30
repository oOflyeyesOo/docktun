package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type templateTransport func(*http.Request) (*http.Response, error)

func (f templateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTemplateImportRestrictsPublicSourcesAndNeverForwardsCredentials(t *testing.T) {
	for _, address := range []string{"http://raw.githubusercontent.com/a/b/main/compose.yml", "https://127.0.0.1/compose.yml", "https://github.com.evil.invalid/a/b/blob/main/compose.yml", "https://user:secret@github.com/a/b/blob/main/compose.yml", "https://github.com/a/b/blob/main/compose.yml?token=secret", "https://raw.githubusercontent.com/a/b/main/../../compose.yml"} {
		if _, err := publicComposeURL(address); err == nil {
			t.Fatalf("accepted non-public source %q", address)
		}
	}
	canonical, err := publicComposeURL("https://github.com/docker/awesome-compose/blob/master/wordpress-mysql/compose.yaml")
	if err != nil || canonical != "https://raw.githubusercontent.com/docker/awesome-compose/master/wordpress-mysql/compose.yaml" {
		t.Fatalf("bad canonical URL: %s %v", canonical, err)
	}
	library := NewTemplateLibrary()
	library.client.Transport = templateTransport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Fatal("credentials forwarded to public template source")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("services:\n  web:\n    image: nginx:alpine\n")), Header: http.Header{}}, nil
	})
	content, source, err := library.Import(context.Background(), canonical)
	if err != nil || !strings.Contains(content, "nginx:alpine") || source != canonical {
		t.Fatalf("import failed: %s %v", source, err)
	}
	redirectURL, _ := url.Parse("http://127.0.0.1/private")
	if err := library.client.CheckRedirect(&http.Request{URL: redirectURL}, nil); err == nil {
		t.Fatal("redirect bypassed source policy")
	}
}

func TestTemplateCatalogPinsSourcesAndCachesIndex(t *testing.T) {
	library := NewTemplateLibrary()
	calls := 0
	library.client.Transport = templateTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(req.URL.Path, "/commits") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","commit":{"tree":{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}]`)), Header: http.Header{}}, nil
		}
		if !strings.HasSuffix(req.URL.Path, "/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
			t.Fatal("catalog did not request the commit's tree")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","tree":[{"type":"blob","path":"wordpress-mysql/compose.yaml"},{"type":"blob","path":".github/compose.yml"},{"type":"blob","path":"other/README.md"}]}`)), Header: http.Header{}}, nil
	})
	for i := 0; i < 2; i++ {
		items, err := library.Catalog(context.Background())
		if err != nil || len(items) != 1 || !strings.Contains(items[0].URL, "/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/") {
			t.Fatalf("unexpected catalog: %+v %v", items, err)
		}
	}
	if calls != 2 {
		t.Fatalf("catalog fetched %d times", calls)
	}
}

func TestComposeEnvironmentExcludesDashboardSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "dashboard-secret")
	t.Setenv("CLOUDFLARE_API_TOKEN", "private-token")
	t.Setenv("DATABASE_URL", "private-database")
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	t.Setenv("DOCKER_API_VERSION", "1.41")
	joined := strings.Join(composeEnvironment(), "\n")
	if strings.Contains(joined, "dashboard-secret") || strings.Contains(joined, "private-token") || strings.Contains(joined, "private-database") || !strings.Contains(joined, "DOCKER_HOST=") {
		t.Fatal("unsafe interpolation environment")
	}
	if !strings.Contains(joined, "DOCKER_API_VERSION=1.41") {
		t.Fatal("explicit older-daemon API compatibility was dropped")
	}
	for _, path := range []string{"/path/to/media", "/your/data/photos", "/data/REPLACE_ME", "/data/<choose-folder>"} {
		if !placeholderMount.MatchString(path) {
			t.Fatalf("missed placeholder %s", path)
		}
	}
	if placeholderMount.MatchString("/volume1/docker/photoprism/originals") {
		t.Fatal("real Synology path treated as a placeholder")
	}
}

func TestReviewedStoragePolicyIsPersistentProjectMetadata(t *testing.T) {
	svc := projectPathService(t)
	p, err := svc.Create("example", t.TempDir(), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if reviewedProject(svc.db, p.ID) {
		t.Fatal("unreviewed project marked reviewed")
	}
	if err := svc.MarkStorageReviewed(p.ID, "https://example.invalid/source"); err != nil {
		t.Fatal(err)
	}
	if !reviewedProject(svc.db, p.ID) {
		t.Fatal("review policy was not persisted")
	}
	if _, err := svc.db.Exec(`INSERT INTO projects(name,path,repo_url,git_username,git_token) SELECT name,path,repo_url,git_username,git_token FROM projects WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if count, err := svc.PruneDuplicates(); err != nil || count != 0 {
		t.Fatalf("cleanup discarded distinct review policy: %d %v", count, err)
	}
	if err := svc.DeleteProject(p.ID, false, false); err != nil {
		t.Fatal(err)
	}
	if reviewedProject(svc.db, p.ID) {
		t.Fatal("deleted project retained its review metadata")
	}
}
