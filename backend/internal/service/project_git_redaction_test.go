package service

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProjectDeploymentRedactsAuthenticatedGitErrorsFromOutputAndAudit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux shell fixture")
	}
	for _, failedStep := range []string{"fetch", "checkout", "pull"} {
		t.Run(failedStep, func(t *testing.T) {
			bin := t.TempDir()
			const token = "private:token@with/slash and?query"
			service := projectPathService(t)
			service.SetRequireMountInspectionForTest(func() bool { return false })
			project, err := service.Create("git-redaction", t.TempDir(), "https://example.invalid/repo.git", "fixture", token)
			if err != nil {
				t.Fatal(err)
			}
			// Load saved credentials through GetByID, as the deployment handler does.
			saved, err := service.GetByID(project.ID)
			if err != nil {
				t.Fatal(err)
			}
			remote := service.buildAuthURL(saved)
			quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
			script := "#!/bin/sh\nif [ \"$1\" = " + quote(failedStep) + " ]; then\n" +
				"printf '%s\\n' " + quote("fatal: could not authenticate "+remote+" token="+token) + " >&2\nexit 1\nfi\nexit 0\n"
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			audit := make(chan string, 1)
			operationID, err := service.DeployStream(project.ID, saved, "main", func(output string) { audit <- output })
			if err != nil {
				t.Fatal(err)
			}
			waitProjectOperation(t, service, project.ID, operationID)
			lines, _, err := service.GetOperationOutput(project.ID, operationID)
			if err != nil {
				t.Fatal(err)
			}
			escaped := strings.TrimPrefix(url.UserPassword("", token).String(), ":")
			for _, output := range []string{strings.Join(lines, "\n"), <-audit} {
				if strings.Contains(output, token) || strings.Contains(output, escaped) {
					t.Fatal("Git credential leaked into operation output or audit callback")
				}
				if !strings.Contains(output, "[ERROR] git "+failedStep+" failed") || !strings.Contains(output, "********") {
					t.Fatalf("missing redacted failure diagnostic: %s", output)
				}
			}
		})
	}
}
