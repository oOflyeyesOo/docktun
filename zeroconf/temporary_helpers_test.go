package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type temporaryHelperTransport func(*http.Request) (*http.Response, error)

func (f temporaryHelperTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTemporaryOwnedHelpersAreNotAdvertised(t *testing.T) {
	manager := &mDNSManager{}
	client := &http.Client{Transport: temporaryHelperTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"Names":["/real-app"]},{"Names":["/docktun-storage-check-user-app"]},{"Names":["/probe"],"Labels":{"docktun.storage-check":"true"}},{"Names":["/updater"],"Labels":{"docktun.update.target":"core"}}]`))}, nil
	})}
	manager.refreshContainers(client)
	if !manager.isKnown("real-app") || !manager.isKnown("docktun-storage-check-user-app") || manager.isKnown("probe") || manager.isKnown("updater") {
		t.Fatal("temporary helper filtering captured an app or advertised a helper")
	}
}
