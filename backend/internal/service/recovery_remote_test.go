package service

import (
	"bytes"
	"context"
	"docktun/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteBackupRequiresReadbackAndRetentionKeepsUnuploaded(t *testing.T) {
	d := testRoadmapDB(t)
	svc := NewRecovery(d, &config.Config{DBPath: filepath.Join(t.TempDir(), "app.db")}, NewOperationStore(d))
	var encrypted bytes.Buffer
	if e := EncryptRecovery(&encrypted, strings.NewReader("fixture backup"), "separate-fixture-passphrase"); e != nil {
		t.Fatal(e)
	}
	first, e := svc.Import(bytes.NewReader(encrypted.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	second, e := svc.Import(bytes.NewReader(encrypted.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	_, e = d.Exec(`UPDATE recovery_backups SET created_at=? WHERE id=?`, time.Now().Add(-72*time.Hour).Unix(), first.ID)
	if e != nil {
		t.Fatal(e)
	}
	var remote []byte
	corrupt := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			remote, _ = io.ReadAll(r.Body)
			w.WriteHeader(201)
			return
		}
		if corrupt {
			w.Write([]byte("wrong object"))
			return
		}
		w.Write(remote)
	}))
	defer server.Close()
	svc.remoteClient = server.Client()
	if e = svc.SaveSettings(RecoverySettings{RemoteURL: server.URL + "/collection", RetainCount: 1, RetainDays: 1}); e != nil {
		t.Fatal(e)
	}
	if e = svc.UploadRemote(context.Background(), first.ID); e == nil {
		t.Fatal("unverified remote object accepted")
	}
	if e = svc.ApplyRetention(time.Now()); e != nil {
		t.Fatal(e)
	}
	if file, e := svc.Download(first.ID); e != nil {
		t.Fatal("unuploaded backup retired", e)
	} else {
		file.Close()
	}
	corrupt = false
	if e = svc.UploadRemote(context.Background(), first.ID); e != nil {
		t.Fatal(e)
	}
	if e = svc.ApplyRetention(time.Now()); e != nil {
		t.Fatal(e)
	}
	if file, e := svc.Download(first.ID); e == nil {
		file.Close()
		t.Fatal("verified old copy not retired")
	}
	if file, e := svc.Download(second.ID); e != nil {
		t.Fatal("newest copy retired")
	} else {
		file.Close()
	}
}
func TestDownloadTicketIsBoundToArchiveAndSingleUse(t *testing.T) {
	d := testRoadmapDB(t)
	svc := NewRecovery(d, &config.Config{DBPath: filepath.Join(t.TempDir(), "app.db")}, NewOperationStore(d))
	var encrypted bytes.Buffer
	EncryptRecovery(&encrypted, strings.NewReader("fixture"), "separate-fixture-passphrase")
	item, e := svc.Import(bytes.NewReader(encrypted.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	ticket, e := svc.DownloadTicket(item.ID, 1)
	if e != nil {
		t.Fatal(e)
	}
	if svc.ConsumeDownload("other", ticket) == nil {
		t.Fatal("cross-archive ticket accepted")
	}
	if e = svc.ConsumeDownload(item.ID, ticket); e != nil {
		t.Fatal(e)
	}
	if svc.ConsumeDownload(item.ID, ticket) == nil {
		t.Fatal("ticket replayed")
	}
}
