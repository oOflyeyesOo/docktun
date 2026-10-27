package service

import (
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

func TestVolumeSizesAcceptCurrentAndLegacyDockerOutput(t *testing.T) {
	cases := []struct {
		input string
		want  map[string]string
	}{
		{`{"Images":[],"Volumes":[{"Name":"data","Size":"4.2MB"},{"Name":"empty","Size":"0B"}]}`, map[string]string{"data": "4.2MB", "empty": "0B"}},
		{"{\"VolumeName\":\"data\",\"Size\":\"2GB\"}\n{\"Name\":\"other\",\"Size\":\"1kB\"}", map[string]string{"data": "2GB", "other": "1kB"}},
		{`{"Volumes":[]}`, map[string]string{}},
		{`invalid`, map[string]string{}},
	}
	for _, tc := range cases {
		got := parseVolumeSizes([]byte(tc.input))
		if len(got) != len(tc.want) {
			t.Fatalf("wrong volume sizes: %v", got)
		}
		for name, size := range tc.want {
			if got[name] != size {
				t.Fatalf("%s: got %s want %s", name, got[name], size)
			}
		}
	}
}

func TestStorageConcurrentScansShareSuccessAndFailureButRefreshIsFresh(t *testing.T) {
	for _, failure := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			var shared sharedStorageScan[int]
			var scans atomic.Int32
			release := make(chan struct{})
			results := make(chan error, 20)
			expectedErr := errors.New("daemon unavailable")
			load := func() ([]int, error) {
				scans.Add(1)
				<-release
				if failure {
					return nil, expectedErr
				}
				return []int{7}, nil
			}
			for i := 0; i < 20; i++ {
				go func() {
					items, err := shared.get(load)
					if !failure && (len(items) != 1 || items[0] != 7) {
						results <- errors.New("wrong shared result")
						return
					}
					results <- err
				}()
			}
			synctest.Wait()
			if scans.Load() != 1 {
				t.Fatalf("concurrent scan duplicated %d times", scans.Load())
			}
			close(release)
			synctest.Wait()
			for i := 0; i < 20; i++ {
				err := <-results
				if failure {
					if !errors.Is(err, expectedErr) {
						t.Fatalf("error lost: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			}
			items, err := shared.get(func() ([]int, error) { scans.Add(1); return []int{9}, nil })
			if err != nil || len(items) != 1 || items[0] != 9 || scans.Load() != 2 {
				t.Fatal("completed or failed scan was cached")
			}
		})
	}
}
