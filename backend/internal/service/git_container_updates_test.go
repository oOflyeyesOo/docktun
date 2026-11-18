package service

import (
	"testing"
)

func TestParseGitHubRepo(t *testing.T) {
	tests := []struct {
		url       string
		wantOwner string
		wantRepo  string
		wantOK    bool
	}{
		{"https://github.com/viren070/aiostreams", "viren070", "aiostreams", true},
		{"https://github.com/viren070/aiostreams.git", "viren070", "aiostreams", true},
		{"http://github.com/oOflyeyesOo/docktun/", "oOflyeyesOo", "docktun", true},
		{"git@github.com:viren070/aiostreams.git", "viren070", "aiostreams", true},
		{"github.com/user/project", "user", "project", true},
		{"ssh://git@github.com/org/repo.git", "org", "repo", true},
		{"https://gitlab.com/user/project", "", "", false},
		{"https://github.com/incomplete", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		owner, repo, ok := parseGitHubRepo(tt.url)
		if ok != tt.wantOK {
			t.Errorf("parseGitHubRepo(%q) ok = %v, want %v", tt.url, ok, tt.wantOK)
		}
		if owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("parseGitHubRepo(%q) = (%q, %q), want (%q, %q)", tt.url, owner, repo, tt.wantOwner, tt.wantRepo)
		}
	}
}

func TestIsLocalOrCustomImage(t *testing.T) {
	tests := []struct {
		image string
		want  bool
	}{
		{"aiostreams", true},
		{"aiostreams:latest", true},
		{"aio-stack_aiostreams", true},
		{"18328305a553", true},
		{"sha256:18328305a5534891b0f123456789abcdef", true},
		{"docktun", true},
		{"docktun:latest", true},
		{"ghcr.io/viren070/aiostreams:latest", false},
		{"docker.io/library/nginx:latest", false},
		{"redis:alpine", true}, // single segment without registry host
		{"viren070/aiostreams:latest", false},
		{"linuxserver/wireguard", false},
		{"vaultwarden/server:latest", false},
		{"quay.io/coreos/etcd", false},
	}

	for _, tt := range tests {
		got := isLocalOrCustomImage(tt.image)
		if got != tt.want {
			t.Errorf("isLocalOrCustomImage(%q) = %v, want %v", tt.image, got, tt.want)
		}
	}
}

func TestIsHexDigest(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"18328305a553", true},
		{"sha256:18328305a553", true},
		{"fe29a16e52", true},
		{"aiostreams", false},
		{"v1.0.0", false},
		{"123", false}, // too short
	}

	for _, tt := range tests {
		got := isHexDigest(tt.val)
		if got != tt.want {
			t.Errorf("isHexDigest(%q) = %v, want %v", tt.val, got, tt.want)
		}
	}
}
