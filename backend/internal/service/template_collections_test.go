package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPortainerConversionPreservesStorageAndRuntimeSettings(t *testing.T) {
	content, err := portainerCompose([]byte(`{"type":1,"title":"Example app","image":"alpine:3.23","description":"A <b>test</b> app","note":"Set up <a href='https://example.com'>the app</a>","network":"private-network","restart_policy":"unless-stopped","privileged":true,"interactive":true,"command":["echo","$TOKEN"],"ports":["8080:80/tcp"],"volumes":[{"container":"/config","bind":"/volume1/docker/example","readonly":true},{"container":"/data"}],"env":[{"name":"PASSWORD"},{"name":"LITERAL","default":"p$word"},{"name":"MODE","select":[{"value":"yes","default":true}]}],"labels":[{"name":"example","value":"$literal"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Services map[string]struct {
			Image       string
			Privileged  bool
			TTY         bool
			StdinOpen   bool `yaml:"stdin_open"`
			Environment map[string]string
			Command     []string
			Volumes     []struct {
				Type, Source, Target string
				ReadOnly             bool `yaml:"read_only"`
				Bind                 struct {
					Create bool `yaml:"create_host_path"`
				}
			}
			Labels   map[string]string
			Networks []string
		}
		Volumes  map[string]any
		Networks map[string]struct {
			External bool
			Name     string
		}
	}
	if err = yaml.Unmarshal([]byte(content), &document); err != nil {
		t.Fatal(err)
	}
	app := document.Services["example-app"]
	if app.Image != "alpine:3.23" || !app.Privileged || !app.TTY || !app.StdinOpen || app.Command[1] != "$$TOKEN" {
		t.Fatalf("runtime settings lost: %+v", app)
	}
	if app.Environment["LITERAL"] != "p$$word" || app.Environment["MODE"] != "yes" || !strings.Contains(app.Environment["PASSWORD"], "?Set PASSWORD") || app.Labels["example"] != "$$literal" {
		t.Fatalf("environment semantics lost: %+v", app.Environment)
	}
	if len(app.Volumes) != 2 || app.Volumes[0].Source != "/volume1/docker/example" || !app.Volumes[0].ReadOnly || app.Volumes[0].Bind.Create || app.Volumes[1].Type != "volume" || len(document.Volumes) != 1 {
		t.Fatalf("storage settings lost: %s", content)
	}
	if !document.Networks["template-network"].External || document.Networks["template-network"].Name != "private-network" || !strings.Contains(content, "# Setup: Set up the app") || strings.Contains(content, "<b>") {
		t.Fatal(content)
	}
	for _, raw := range []string{`{"type":1,"title":"x","image":"alpine","cap_add":["NET_ADMIN"]}`, `{"type":1,"image":"alpine","env":[{"name":"A}: bad"}]}`} {
		if _, err := portainerCompose([]byte(raw)); err == nil {
			t.Fatalf("silently accepted unsupported settings: %s", raw)
		}
	}
}

func TestPortainerCatalogAndStackImportPinBothRepositories(t *testing.T) {
	library := NewTemplateLibrary()
	calls := 0
	fixture := `{"version":"2","templates":[{"type":1,"title":"App","image":"alpine","description":"An <b>app</b>","categories":["Tools"]},{"type":2,"title":"Swarm"},{"type":1,"title":"Windows","image":"windows","platform":"windows"},{"type":3,"title":"Stack","repository":{"url":"https://github.com/example/stacks","stackfile":"app/compose.yml"},"note":"Choose storage"}]}`
	library.client.Transport = templateTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Fatal("forwarded credentials")
		}
		body := ""
		switch req.URL.Path {
		case "/repos/example/catalog/commits/main":
			body = `{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
		case "/example/catalog/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/templates.json":
			body = fixture
		case "/repos/example/stacks/commits/HEAD":
			body = `{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`
		case "/example/stacks/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/app/compose.yml":
			body = "services:\n  app:\n    image: alpine\n"
		default:
			t.Fatalf("unexpected source %s", req.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	items, err := library.PortainerCatalog(context.Background(), "https://github.com/example/catalog/blob/main/templates.json")
	if err != nil || len(items) != 4 {
		t.Fatalf("catalog: %+v %v", items, err)
	}
	if items[0].Description != "An app" || !strings.Contains(items[0].Collection, "/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/") || items[1].Unavailable == "" || items[2].Unavailable == "" {
		t.Fatalf("missing descriptions/capability information: %+v", items)
	}
	content, source, err := library.ImportPortainer(context.Background(), items[0].Collection, items[0].ID)
	if err != nil || !strings.Contains(content, "alpine") || !strings.Contains(source, items[0].ID) || calls != 2 {
		t.Fatalf("cached import: %s %v %d", content, err, calls)
	}
	content, _, err = library.ImportPortainer(context.Background(), items[3].Collection, items[3].ID)
	if err != nil || !strings.Contains(content, "/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/") || !strings.Contains(content, "Choose storage") || calls != 4 {
		t.Fatalf("stack revision lost: %s %v", content, err)
	}
	if _, _, err = library.ImportPortainer(context.Background(), items[0].Collection, "changed"); err == nil {
		t.Fatal("accepted stale selection")
	}
	if _, _, err = library.ImportPortainer(context.Background(), items[1].Collection, items[1].ID); err == nil {
		t.Fatal("accepted Swarm")
	}
	for _, address := range []string{"https://127.0.0.1/templates.json", "https://raw.githubusercontent.com.evil.invalid/a/b/main/templates.json", "https://user:secret@raw.githubusercontent.com/a/b/main/templates.json", "https://raw.githubusercontent.com/a/b/main/templates.json?token=x"} {
		if _, err = library.PortainerCatalog(context.Background(), address); err == nil {
			t.Fatalf("accepted private source %s", address)
		}
	}
}

func TestPortainerHostNetworkDoesNotPublishIncompatiblePorts(t *testing.T) {
	content, err := portainerCompose([]byte(`{"type":1,"image":"alpine","title":"Host app","network":"host","ports":["8080:80"]}`))
	if err != nil || strings.Contains(content, "ports:") || !strings.Contains(content, "network_mode: host") {
		t.Fatalf("invalid host networking: %s %v", content, err)
	}
}
