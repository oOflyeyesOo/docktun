package service

import (
	"encoding/json"
	"testing"
)

func TestDatabaseMajorDetectsAliasedImagesAndMissingVersions(t *testing.T) {
	for _, tc := range []struct {
		image         string
		env           []string
		family, major string
	}{
		{"timescale/timescaledb:latest-pg17", []string{"PG_MAJOR=17"}, "postgres", "17"},
		{"company/renamed-db:next", []string{"MYSQL_MAJOR=8.4"}, "mysql", "8.4"},
		{"postgres:latest", nil, "postgres", ""},
		{"company/application:latest", nil, "", ""},
	} {
		family, major := databaseMajor(tc.image, tc.env)
		if family != tc.family || major != tc.major {
			t.Fatalf("%s detected %s/%s, expected %s/%s", tc.image, family, major, tc.family, tc.major)
		}
	}
}

func TestCloudflareMigrationOwnershipRequiresManagedIdentityOrSharedTrustedImage(t *testing.T) {
	core := managedFixture("docktun-core", nil)
	core.Mounts = append(core.Mounts, struct {
		Type, Source, Name, Destination, Mode, Propagation string
		RW                                                 bool
	}{Type: "volume", Name: "instance-config", Destination: "/etc/cloudflared", RW: true})
	for _, tc := range []struct {
		name, image, volume string
		owned               bool
	}{
		{"legacy official", "cloudflare/cloudflared:latest", "instance-config", true},
		{"canonical official", "docker.io/cloudflare/cloudflared:latest", "instance-config", true},
		{"neighbor state", "cloudflare/cloudflared:latest", "neighbor-config", false},
		{"unrelated image", "company/application:latest", "instance-config", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := managedFixture("docktun-cloudflared", nil)
			existing.Config["Image"] = tc.image
			if err := json.Unmarshal([]byte(`{"Mounts":[{"Type":"volume","Name":"`+tc.volume+`","Destination":"/etc/cloudflared"}]}`), &existing); err != nil {
				t.Fatal(err)
			}
			if got := cloudflareMigrationOwned(existing, core, "/etc/cloudflared/config.yml"); got != tc.owned {
				t.Fatalf("ownership=%v, expected %v", got, tc.owned)
			}
		})
	}
}
