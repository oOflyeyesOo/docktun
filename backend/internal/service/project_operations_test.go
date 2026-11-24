package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProjectOperationsRejectOverlapWithoutReplacingOutput(t *testing.T) {
	service := projectPathService(t)
	service.SetRequireMountInspectionForTest(func() bool { return false })
	operationID, err := service.beginOutput(7)
	if err != nil || operationID == "" {
		t.Fatalf("first operation: %q %v", operationID, err)
	}
	service.appendOutput(7, "original operation")

	var workers sync.WaitGroup
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := service.ComposeAction(7, "", nil, "down"); !errors.Is(err, ErrProjectBusy) {
				t.Errorf("overlap was admitted: %v", err)
			}
		}()
	}
	workers.Wait()
	if _, err := service.DeployStream(7, Project{}, "", nil); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("deployment overlapped: %v", err)
	}
	if err := service.DeleteProject(7, true, true); !errors.Is(err, ErrProjectBusy) {
		t.Fatalf("deletion overlapped: %v", err)
	}
	if _, err := service.beginOutput(8); err != nil {
		t.Fatalf("unrelated project was rejected: %v", err)
	}
	lines, done, err := service.GetOperationOutput(7, operationID)
	if err != nil || done || !reflect.DeepEqual(lines, []string{"original operation"}) {
		t.Fatalf("rejected actions replaced output: %v %v %v", lines, done, err)
	}
	service.finishOutput(7, nil)
	service.finishOutput(8, nil)
}

func TestIndependentProjectActionsProceedWhileComposeIsBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux shell fixture")
	}
	bin, firstDir, secondDir := t.TempDir(), t.TempDir(), t.TempDir()
	entered, release := filepath.Join(bin, "entered"), filepath.Join(bin, "release")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nblocked=false\nfor arg in \"$@\"; do\n [ \"$arg\" = " + quote(firstDir) + " ] && blocked=true\n case \"$arg\" in\n config) echo '{\"services\":{\"app\":{\"image\":\"fixture\"}}}'; exit 0 ;;\n up) if $blocked; then touch " + quote(entered) + "; while [ ! -f " + quote(release) + " ]; do sleep 0.01; done; fi; echo started; exit 0 ;;\n esac\ndone\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0600) })
	for _, directory := range []string{firstDir, secondDir} {
		if err := os.WriteFile(filepath.Join(directory, "compose.yml"), []byte("services:\n  app:\n    image: fixture\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	service := projectPathService(t)
	service.SetRequireMountInspectionForTest(func() bool { return false })
	first, err := service.Create("blocked-project", firstDir, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create("independent-project", secondDir, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	firstOperation, err := service.ComposeAction(first.ID, first.Path, nil, "up", "-d")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(entered); err == nil {
			break
		}
		if time.Now().After(deadline) {
			lines, _, _ := service.GetOperationOutput(first.ID, firstOperation)
			t.Fatalf("first action did not block in Compose: %v", lines)
		}
		time.Sleep(time.Millisecond)
	}
	secondOperation, err := service.ComposeAction(second.ID, second.Path, nil, "up", "-d")
	if err != nil {
		t.Fatal(err)
	}
	waitProjectOperation(t, service, second.ID, secondOperation)
	if _, done, _ := service.GetOperationOutput(first.ID, firstOperation); done {
		t.Fatal("first action should still be blocked")
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitProjectOperation(t, service, first.ID, firstOperation)
}

func TestProjectOperationSnapshotsStayBoundToTheirOperation(t *testing.T) {
	service := NewProjects(nil, "")
	first, err := service.beginOutput(3)
	if err != nil {
		t.Fatal(err)
	}
	service.appendOutput(3, "first")
	service.finishOutput(3, func(output string) {
		if output != "first" {
			t.Fatalf("callback saw wrong output %q", output)
		}
		if _, err := service.beginOutput(3); !errors.Is(err, ErrProjectBusy) {
			t.Fatalf("released operation before its callback finished: %v", err)
		}
	})
	second, err := service.beginOutput(3)
	if err != nil || first == second {
		t.Fatalf("second operation: %q %v", second, err)
	}
	service.appendOutput(3, "second")
	lines, done, err := service.GetOperationOutput(3, first)
	if err != nil || !done || !reflect.DeepEqual(lines, []string{"first"}) {
		t.Fatalf("first observer read second output: %v %v %v", lines, done, err)
	}
	lines[0] = "mutated"
	lines, _, _ = service.GetOperationOutput(3, first)
	if lines[0] != "first" {
		t.Fatal("observer modified retained output")
	}
	if _, _, err := service.GetOperationOutput(4, first); !errors.Is(err, ErrProjectOperationNotFound) {
		t.Fatalf("operation crossed project boundary: %v", err)
	}
	current, done := service.GetOutput(3)
	if done || !reflect.DeepEqual(current, []string{"second"}) {
		t.Fatalf("legacy current output changed: %v %v", current, done)
	}
	service.finishOutput(3, nil)
}

func TestProjectOperationHistoryIsBoundedWithoutEvictingActiveOutput(t *testing.T) {
	service := NewProjects(nil, "")
	active, err := service.beginOutput(1)
	if err != nil {
		t.Fatal(err)
	}
	var oldest, latest string
	for index := 0; index <= completedProjectOperationLimit; index++ {
		latest, err = service.beginOutput(2)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			oldest = latest
		}
		service.appendOutput(2, fmt.Sprintf("operation %d", index))
		service.finishOutput(2, nil)
	}
	if _, _, err := service.GetOperationOutput(2, oldest); !errors.Is(err, ErrProjectOperationNotFound) {
		t.Fatalf("expired operation retained: %v", err)
	}
	if _, _, err := service.GetOperationOutput(2, latest); err != nil {
		t.Fatal("latest operation lost", err)
	}
	if _, done, err := service.GetOperationOutput(1, active); err != nil || done {
		t.Fatalf("active operation evicted: %v %v", done, err)
	}
	if len(service.operations) != completedProjectOperationLimit+1 {
		t.Fatalf("unbounded retained history: %d", len(service.operations))
	}
	service.finishOutput(1, nil)
}

func waitProjectOperation(t *testing.T, service *Projects, projectID int, operationID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, done, err := service.GetOperationOutput(projectID, operationID)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not finish")
}

func TestProjectOperationOutputIsBoundedAndRetainsEvictedErrors(t *testing.T) {
	service := NewProjects(nil, "")
	operationID, err := service.beginOutput(1)
	if err != nil {
		t.Fatal(err)
	}
	service.appendOutput(1, "[ERROR] original command failed")
	service.appendOutput(1, strings.Repeat("tail", projectOperationOutputLimit))
	lines, _, err := service.GetOperationOutput(1, operationID)
	if err != nil || len(lines) != 2 || !strings.Contains(lines[0], "[ERROR]") {
		t.Fatalf("truncation lost failure status: %d %v", len(lines), err)
	}
	if len(lines[1]) != projectOperationOutputLimit || service.outputs[1].Bytes > projectOperationOutputLimit {
		t.Fatal("oversized line escaped output bound")
	}
	service.appendOutput(1, "latest output")
	lines, _ = service.GetOutput(1)
	if len(lines) != 2 || lines[1] != "latest output" || !strings.Contains(lines[0], "[ERROR]") {
		t.Fatalf("tail or failure indicator lost after eviction: %v", lines)
	}
	service.finishOutput(1, func(output string) {
		if !strings.Contains(output, "[ERROR]") {
			t.Fatal("completion callback reported false success")
		}
	})
	if _, err := service.beginOutput(1); err != nil {
		t.Fatal(err)
	}
	service.appendOutput(1, strings.Repeat("x", projectOperationOutputLimit+1))
	lines, _ = service.GetOutput(1)
	if len(lines) != 2 || !strings.Contains(lines[0], "[WARN]") || strings.Contains(lines[0], "[ERROR]") {
		t.Fatal("new operation inherited an earlier failure")
	}
	for range projectOperationLineLimit + 1 {
		service.appendOutput(1, "")
	}
	if len(service.outputs[1].Lines) > projectOperationLineLimit {
		t.Fatal("empty output bypassed the line bound")
	}
	service.finishOutput(1, nil)
}

func TestProjectDeploymentStartsOnlyAfterSuccessfulPull(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux shell fixture")
	}
	for _, failPull := range []bool{false, true} {
		t.Run(fmt.Sprintf("pull_fails_%v", failPull), func(t *testing.T) {
			bin, directory := t.TempDir(), t.TempDir()
			calls := filepath.Join(t.TempDir(), "calls")
			script := `#!/bin/sh
[ "$1" = compose ] || exit 1
for arg in "$@"; do
 case "$arg" in
  config) printf '%s\n' '{"services":{"app":{"image":"fixture:latest"}}}'; exit 0 ;;
  pull|up)
   printf '%s\n' "$arg" >> "$FIXTURE_CALLS"
   if [ "$arg" = pull ] && [ "$FIXTURE_PULL_FAIL" = true ]; then
    echo 'fixture image download failed' >&2
    exit 1
   fi
   echo "fixture $arg finished"
   exit 0 ;;
 esac
done
exit 1
`
			quotedCalls := "'" + strings.ReplaceAll(calls, "'", "'\"'\"'") + "'"
			script = strings.ReplaceAll(script, "\"$FIXTURE_CALLS\"", quotedCalls)
			script = strings.ReplaceAll(script, "\"$FIXTURE_PULL_FAIL\"", fmt.Sprint(failPull))
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := os.WriteFile(filepath.Join(directory, "compose.yml"), []byte("services:\n  app:\n    image: fixture:latest\n"), 0644); err != nil {
				t.Fatal(err)
			}
			service := projectPathService(t)
			service.SetRequireMountInspectionForTest(func() bool { return false })
			project, err := service.Create("deploy-safety", directory, "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			operationID, err := service.DeployStream(project.ID, project, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			waitProjectOperation(t, service, project.ID, operationID)
			recorded, err := os.ReadFile(calls)
			if err != nil {
				lines, _, _ := service.GetOperationOutput(project.ID, operationID)
				t.Fatalf("deployment did not reach pull: %v, output %v", err, lines)
			}
			expected := "pull\nup\n"
			if failPull {
				expected = "pull\n"
			}
			if string(recorded) != expected {
				t.Fatalf("unexpected deployment commands %q, expected %q", recorded, expected)
			}
			lines, _, err := service.GetOperationOutput(project.ID, operationID)
			if err != nil {
				t.Fatal(err)
			}
			output := strings.Join(lines, "\n")
			if failPull && (!strings.Contains(output, "[ERROR]") || strings.Contains(output, "Starting containers")) {
				t.Fatalf("failed pull was reported as a successful deployment: %s", output)
			}
		})
	}
}
