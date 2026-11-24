package service

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunCommandRedactionCoversEnvironmentSyntax(t *testing.T) {
	for _, syntax := range []string{
		`-e PASSWORD=unmasked-value`, `--env PASSWORD=unmasked-value`,
		`--env=PASSWORD=unmasked-value`, `-e=PASSWORD=unmasked-value`,
		`-ePASSWORD=unmasked-value`, `-e "PASSWORD=unmasked value"`,
		`--env 'PASSWORD=unmasked value'`, `--env=PASSWORD="unmasked value"`,
		`-e PASSWORD=unmasked\ value`,
	} {
		t.Run(syntax, func(t *testing.T) {
			command := "docker run " + syntax + " -e INHERITED alpine"
			redacted := RedactDockerRunCommand(command)
			if strings.Contains(redacted, "unmasked") || !strings.Contains(redacted, "PASSWORD="+runRedacted) || !strings.Contains(redacted, "INHERITED") {
				t.Fatalf("environment value exposed or command lost: %s", redacted)
			}
		})
	}
	args := []string{"run", "--env=PASSWORD=unmasked-value", "alpine"}
	original := append([]string(nil), args...)
	_ = RedactDockerRunArgs(args)
	if !reflect.DeepEqual(args, original) {
		t.Fatal("redaction changed execution arguments")
	}
}

func TestRunOutputRedactsCredentialEchoAndPreservesOrdinaryShortValues(t *testing.T) {
	r := NewRunCommandRedactor(`docker run -e "PASSWORD=private value" --env=TOKEN=fixture-token -e FLAG=1 alpine`)
	output := r.Redact("PASSWORD=private value TOKEN=fixture-token private value fixture-token: process1 started FLAG=1")
	if strings.Contains(output, "private value") || strings.Contains(output, "fixture-token") {
		t.Fatalf("credential echo exposed: %s", output)
	}
	if !strings.Contains(output, "process1 started") || !strings.Contains(output, "FLAG="+runRedacted) {
		t.Fatalf("ordinary output corrupted: %s", output)
	}
	r = NewRunCommandRedactor(`docker run -e PASSWORD=abc -e TOKEN=x -e FLAG=1 alpine`)
	output = r.Redact("abc x abc123 abcabc processx FLAG=1")
	if output != runRedacted+" "+runRedacted+" abc123 abcabc processx FLAG="+runRedacted {
		t.Fatalf("short credential redaction corrupted unrelated words: %s", output)
	}
	for _, key := range []string{"AUTH", "PASS", "PWD"} {
		r = NewRunCommandRedactor("docker run -e " + key + "=private-value alpine")
		if output := r.Redact("private-value"); output != runRedacted {
			t.Fatalf("common credential key %s was not recognized: %s", key, output)
		}
	}
}

type fragmentedRunOutput struct{ io.Reader }

func (r fragmentedRunOutput) Read(target []byte) (int, error) {
	if len(target) > 5 {
		target = target[:5]
	}
	return r.Reader.Read(target)
}

func TestRunOutputCoalescesCredentialFragmentsAndDrainsOversizedLines(t *testing.T) {
	r := NewRunCommandRedactor(`docker run -e PASSWORD=private-fixture-secret alpine`)
	var lines []string
	input := "PASSWORD=private-fixture-secret\n" + strings.Repeat("x", 70<<10) + "\nfinished\n"
	streamRunOutput(fragmentedRunOutput{strings.NewReader(input)}, func(line string) {
		lines = append(lines, r.Redact(line))
	})
	if len(lines) != 3 || lines[0] != "PASSWORD="+runRedacted || !strings.Contains(lines[1], "oversized") || lines[2] != "finished" {
		t.Fatalf("fragment or line-limit handling failed: %#v", lines)
	}
	// The handler redacts the completion snapshot again before persistence.
	r = NewRunCommandRedactor(`docker run -e TOKEN=acted alpine`)
	if output := r.Redact(r.Redact("acted")); output != runRedacted {
		t.Fatalf("redaction was not idempotent: %q", output)
	}
}

func TestRunCompletionSnapshotSurvivesJobPruning(t *testing.T) {
	s := &DockerRunService{jobs: map[int]*DockerRunJob{1: {ID: 1, Lines: []string{"finished"}}}}
	output := s.markDone(1, 0, "")
	for i := 2; i <= 52; i++ {
		s.jobs[i] = &DockerRunJob{ID: i}
	}
	s.pruneOldJobsLocked()
	if _, present := s.GetJob(1); present || output != "finished" {
		t.Fatalf("completion snapshot unavailable after pruning: present=%v output=%q", present, output)
	}
}

func TestRunAdmissionSnapshotSurvivesFastCompletionAndPruning(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // Execution fails immediately without real Docker.
	s := NewDockerRun(nil, nil)
	s.runDocker = func(context.Context, ...string) ([]byte, error) {
		return nil, fmt.Errorf("No such container: admission-fixture")
	}
	response, err := s.StartRun("docker run --name admission-fixture alpine", "direct", false, false, "", nil)
	if err != nil || response == nil {
		t.Fatalf("missing admission response: %#v %v", response, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		job, _ := s.GetJob(response.ID)
		if job.Done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fast failing worker did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	s.mu.Lock()
	for i := 2; i <= 52; i++ {
		s.jobs[i] = &DockerRunJob{ID: i}
	}
	s.pruneOldJobsLocked()
	s.mu.Unlock()
	if _, present := s.GetJob(response.ID); present || response.ID != 1 || response.Done || response.Status != "running" || len(response.Lines) != 0 {
		t.Fatalf("admission snapshot was shared or lost: present=%v response=%#v", present, response)
	}
}
