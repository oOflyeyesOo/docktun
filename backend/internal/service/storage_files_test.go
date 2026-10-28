package service

import "testing"

func TestCleanRelPathRejectsTraversal(t *testing.T) {
	for _, path := range []string{"../secret", "nested/../../secret", `nested\..\secret`, "/../secret", "a\x00b"} {
		if _, err := cleanRelPath(path); err == nil {
			t.Errorf("cleanRelPath(%q) accepted unsafe path", path)
		}
	}
}

func TestCleanRelPathNormalizesSafePath(t *testing.T) {
	got, err := cleanRelPath(`nested\folder/file.txt`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "nested/folder/file.txt" {
		t.Fatalf("cleanRelPath returned %q", got)
	}
}
