package service

import (
	"archive/tar"
	"bytes"
	"context"
	"docktun/internal/config"
	"docktun/internal/db"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testRoadmapDB(t *testing.T) *db.DB {
	t.Helper()
	d, e := db.New(filepath.Join(t.TempDir(), "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
func TestOperationLeaseSurvivesRestartUntilAcknowledged(t *testing.T) {
	d := testRoadmapDB(t)
	a := NewOperationStore(d)
	b := NewOperationStore(d)
	job, e := a.Begin("backup", "maintenance:recovery", map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Begin("update", "container:app", map[string]any{}); e == nil {
		t.Fatal("overlapping update permitted")
	}
	if e = b.ReconcileInterrupted(); e != nil {
		t.Fatal(e)
	}
	items, _ := a.List("")
	if items[0].Phase != "preparing" {
		t.Fatal("same process treated as restarted")
	}
	b.Owner = "new-process"
	if e = b.ReconcileInterrupted(); e != nil {
		t.Fatal(e)
	}
	items, _ = b.List("")
	if items[0].Phase != "interrupted" {
		t.Fatal(items)
	}
	if _, e = b.Begin("update", "container:app", map[string]any{}); e == nil {
		t.Fatal("interrupted lease lost")
	}
	if e = b.Acknowledge(job.ID); e != nil {
		t.Fatal(e)
	}
	app, e := b.Begin("update", "container:app", map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Begin("backup", "maintenance:recovery", map[string]any{}); e == nil {
		t.Fatal("maintenance overlapped app update")
	}
	if e = b.Finish(app.ID, nil); e != nil {
		t.Fatal(e)
	}
}
func TestAlertOutboxRestartRetryAndRecovery(t *testing.T) {
	d := testRoadmapDB(t)
	q := AlertQueue{d}
	events := []QueuedIncident{{"container_stop", "app", "App stopped"}}
	if e := q.Apply(events, `{"state":"down"}`, 100); e != nil {
		t.Fatal(e)
	}
	if e := q.Apply(events, `{"state":"down"}`, 100); e != nil {
		t.Fatal(e)
	}
	pending, _ := q.List(true, 100)
	if len(pending) != 1 {
		t.Fatal(pending)
	}
	id := pending[0].ID
	if e := q.Result(id, errors.New("private upstream secret"), 100); e != nil {
		t.Fatal(e)
	}
	q = AlertQueue{d}
	state, _ := q.State()
	if state != `{"state":"down"}` {
		t.Fatal(state)
	}
	pending, _ = q.List(true, 129)
	if len(pending) != 0 {
		t.Fatal("backoff ignored")
	}
	pending, _ = q.List(true, 130)
	if len(pending) != 1 || pending[0].LastError == "private upstream secret" {
		t.Fatal(pending)
	}
	for i := 1; i < 8; i++ {
		if e := q.Result(id, errors.New("fail"), 130); e != nil {
			t.Fatal(e)
		}
	}
	history, _ := q.List(false, 1000)
	if history[0].Phase != "failed" {
		t.Fatal(history)
	}
	if e := q.Retry(id); e != nil {
		t.Fatal(e)
	}
	if e := q.Apply([]QueuedIncident{{"recovery", "app", "App recovered"}}, `{"state":"up"}`, 200); e != nil {
		t.Fatal(e)
	}
	history, _ = q.List(false, 1000)
	for _, item := range history {
		if item.ID == id && item.Phase != "superseded" {
			t.Fatal("stale incident delivered", item)
		}
	}
}
func TestReadinessRejectsRedirectsCredentialsAndFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(204)
		case "/redirect":
			http.Redirect(w, r, "/ok", 302)
		default:
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	if e := probeReadiness(context.Background(), server.URL+"/ok"); e != nil {
		t.Fatal(e)
	}
	for _, url := range []string{server.URL + "/redirect", server.URL + "/bad", "http://user:secret@localhost/", "file:///etc/passwd"} {
		if probeReadiness(context.Background(), url) == nil {
			t.Fatal("accepted unsafe/not-ready endpoint", url)
		}
	}
}
func TestRecoveryExtractionRejectsTraversalAndExistingDestination(t *testing.T) {
	for _, header := range []*tar.Header{{Name: "../outside", Typeflag: tar.TypeReg}, {Name: "/absolute", Typeflag: tar.TypeReg}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}, {Name: "device", Typeflag: tar.TypeChar}} {
		var archive bytes.Buffer
		tw := tar.NewWriter(&archive)
		if e := tw.WriteHeader(header); e != nil {
			t.Fatal(e)
		}
		tw.Close()
		if ExtractRecoveryVolume(t.TempDir(), &archive) == nil {
			t.Fatal("accepted unsafe tar", header.Name)
		}
	}
	directory := t.TempDir()
	os.WriteFile(filepath.Join(directory, "existing"), []byte("keep"), 0600)
	if ExtractRecoveryVolume(directory, bytes.NewReader(nil)) == nil {
		t.Fatal("accepted nonempty destination")
	}
}
func TestRecoverySettingsNeverPersistPassphrase(t *testing.T) {
	d := testRoadmapDB(t)
	svc := NewRecovery(d, &config.Config{DBPath: filepath.Join(t.TempDir(), "app.db")}, NewOperationStore(d))
	settings := RecoverySettings{RetainCount: 3, RetainDays: 7, Selection: RecoveryRequest{Instance: true, Passphrase: "must-never-persist"}}
	if e := svc.SaveSettings(settings); e != nil {
		t.Fatal(e)
	}
	var raw string
	d.QueryRow(`SELECT value FROM recovery_settings`).Scan(&raw)
	if bytes.Contains([]byte(raw), []byte("must-never-persist")) {
		t.Fatal("persisted recovery key")
	}
	if _, e := d.Exec(`UPDATE recovery_settings SET value='invalid'`); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.Settings(); e == nil {
		t.Fatal("silently accepted corrupted settings")
	}
}
func TestDatabaseMajorFromImmutableImageMetadata(t *testing.T) {
	for _, item := range []struct {
		env           []string
		engine, major string
	}{{[]string{"PG_MAJOR=17"}, "postgres", "17"}, {[]string{"MYSQL_MAJOR=8.4", "MYSQL_VERSION=8.4.7"}, "mysql", "8.4"}, {[]string{"MARIADB_MAJOR=11.4", "MARIADB_VERSION=1:11.4.5"}, "mariadb", "11.4"}} {
		engine, major := databaseMajor("sha256:immutable", item.env)
		if engine != item.engine || major != item.major {
			t.Fatalf("%v: %s %s", item, engine, major)
		}
	}
}

func TestPartialRecoveryKeepsMaintenanceLease(t *testing.T) {
	d := testRoadmapDB(t)
	store := NewOperationStore(d)
	job, err := store.Begin("restore", "maintenance:recovery", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(job.ID, &operationRecoveryRequired{errors.New("partial SQL restore")}); err != nil {
		t.Fatal(err)
	}
	items, _ := store.List("")
	if items[0].Phase != "interrupted" {
		t.Fatal(items)
	}
	if _, err = store.Begin("update", "container:app", map[string]any{}); err == nil {
		t.Fatal("partial recovery released maintenance lease")
	}
	if err = store.Acknowledge(job.ID); err != nil {
		t.Fatal(err)
	}
}
func TestVolumeAbsenceRequiresExplicitNotFound(t *testing.T) {
	if !recoveryVolumeMissing(&DockerAPIError{Status: 404}) {
		t.Fatal("404 not recognized")
	}
	for _, err := range []error{errors.New("daemon unavailable"), &DockerAPIError{Status: 500}, &DockerAPIError{Status: 403}} {
		if recoveryVolumeMissing(err) {
			t.Fatal("Docker access failure treated as an unused volume name")
		}
	}
}
func TestUpdateChannelPersistsWithoutInstalling(t *testing.T) {
	t.Setenv("DOCKTUN_UPDATE_IMAGE", "")
	directory := t.TempDir()
	updater := NewSelfUpdater(directory)
	if updater.Channel().Channel != "stable" {
		t.Fatal(updater.Channel())
	}
	if err := updater.SaveChannel("beta"); err != nil {
		t.Fatal(err)
	}
	if got := NewSelfUpdater(directory).Channel(); got.Channel != "beta" || got.Image != "ghcr.io/ooflyeyesoo/docktun:beta" {
		t.Fatal(got)
	}
	t.Setenv("DOCKTUN_UPDATE_IMAGE", "sha256:offline")
	if got := updater.Channel(); !got.Override || got.Image != "sha256:offline" || got.Signed {
		t.Fatal(got)
	}
	if updater.SaveChannel("untrusted") == nil {
		t.Fatal("invalid channel accepted")
	}
}
