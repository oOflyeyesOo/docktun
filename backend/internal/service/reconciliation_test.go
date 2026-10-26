package service

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestUpdatePreservesConfiguration(t *testing.T) {
	var c updateContainer
	if err := json.Unmarshal([]byte(`{"Id":"0123456789abcdef0123456789abcdef","Name":"/custom-docktun","Config":{"Env":["SECRET=literal $value with spaces"],"Cmd":["server"],"Labels":{"custom":"kept"}},"HostConfig":{"Binds":["/host/data:/app/data:rw"],"VolumesFrom":["source"],"PortBindings":{"3001/tcp":[{"HostIp":"127.0.0.1","HostPort":"8989"}]},"RestartPolicy":{"Name":"unless-stopped"}},"Mounts":[{"Type":"bind","Source":"/host/data","Destination":"/app/data","RW":true},{"Type":"volume","Name":"anonymous-volume-id","Destination":"/extra","RW":true}],"NetworkSettings":{"Networks":{"custom":{"NetworkID":"generated","IPAddress":"172.0.0.2","Aliases":["0123456789ab","docktun-backend"]}}}}`), &c); err != nil {
		t.Fatal(err)
	}
	body := updateCreateBody(c, "sha256:new")
	raw, _ := json.Marshal(body)
	text := string(raw)
	for _, want := range []string{"anonymous-volume-id:/extra:rw", "/host/data:/app/data:rw", "127.0.0.1", "8989", "docktun-backend", "SECRET=literal $value with spaces", "DOCKTUN_CONTAINER=custom-docktun"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	for _, bad := range []string{"VolumesFrom", "NetworkID", "IPAddress", "0123456789ab"} {
		if strings.Contains(text, bad) {
			t.Fatalf("copied generated state %s", bad)
		}
	}
	c.HostConfig["AutoRemove"] = true
	if validateUpdateContainer(c) == nil {
		t.Fatal("auto-remove would destroy rollback")
	}
}

func TestStorageTransferBinaryAndTraversal(t *testing.T) {
	setup := setupFixture(t)
	dir := t.TempDir()
	result, err := setup.db.Exec(`INSERT INTO projects (name,path) VALUES (?,?)`, "transfer", dir)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	svc := NewStorageFilesService(setup.db, setup.cfg)
	target := strconv.FormatInt(id, 10)
	data := string([]byte{0, 255, 1, 2, 10})
	name := `quotes " $(touch injected) ü.bin`
	if err = svc.UploadFile("project", target, "nested", name, strings.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	stream, gotName, size, err := svc.GetDownloadStream("project", target, "nested/"+name)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(stream)
	stream.Close()
	if string(got) != data || gotName != name || size != int64(len(data)) {
		t.Fatal("binary transfer changed")
	}
	if err = svc.UploadFile("project", target, "../outside", "file", strings.NewReader("bad")); err == nil {
		t.Fatal("accepted traversal")
	}
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(dir, "escape")); err == nil {
		if err = svc.UploadFile("project", target, "escape", "file", strings.NewReader("bad")); err == nil {
			t.Fatal("followed symlink outside root")
		}
	}
	if _, _, _, err = svc.GetDownloadStream("project", target, "nested"); err == nil {
		t.Fatal("downloaded directory")
	}
}
