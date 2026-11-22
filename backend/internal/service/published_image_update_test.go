package service

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestPublishedImageIDUsesPlatformConfigDigest(t *testing.T) {
	fetch := func(ref string) (imageUpdateManifest, error) {
		var doc imageUpdateManifest
		switch ref {
		case ":latest":
			err := json.Unmarshal([]byte(`{"manifests":[
				{"digest":"sha256:attestation","platform":{"os":"unknown","architecture":"unknown"}},
				{"digest":"sha256:amd-manifest","platform":{"os":"linux","architecture":"amd64"}},
				{"digest":"sha256:arm-manifest","platform":{"os":"linux","architecture":"arm64","variant":"v8"}}
			]}`), &doc)
			return doc, err
		case "@sha256:arm-manifest", ":single":
			doc.Config.Digest = "sha256:running-image"
			return doc, nil
		default:
			return doc, fmt.Errorf("unexpected registry request %s", ref)
		}
	}
	platform := recoveryImageInfo{OS: "linux", Architecture: "arm64"}
	for _, ref := range []string{":latest", ":single"} {
		got, err := publishedImageID(fetch, ref, platform)
		if err != nil || got != "sha256:running-image" {
			t.Fatalf("%s: got %q, %v", ref, got, err)
		}
	}
	platform.Architecture = "s390x"
	if _, err := publishedImageID(fetch, ":latest", platform); err == nil {
		t.Fatal("expected an error for an unpublished platform")
	}
}
