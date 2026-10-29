package service

import (
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestContainerStatsNeverBlockAndShareOneBackgroundSample(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var calls atomic.Int32
		svc := &DockerContainers{statsCollector: func() map[string]liveStats {
			n := calls.Add(1)
			<-release
			return map[string]liveStats{"app": {cpu: fmt.Sprint(n) + "%"}}
		}}
		for i := 0; i < 20; i++ {
			if got := svc.getCachedStats(); len(got) != 0 {
				t.Fatal("invented usage before first sample")
			}
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatalf("duplicated sample: %d", calls.Load())
		}
		close(release)
		synctest.Wait()
		sample := svc.getCachedStats()
		if sample["app"].cpu != "1%" {
			t.Fatal("completed sample unavailable")
		}
		sample["app"] = liveStats{cpu: "modified"}
		if svc.getCachedStats()["app"].cpu != "1%" {
			t.Fatal("caller changed cached sample")
		}
		time.Sleep(7 * time.Second)
		if svc.getCachedStats()["app"].cpu != "1%" {
			t.Fatal("refresh did not return the existing sample immediately")
		}
		synctest.Wait()
		if calls.Load() != 2 || svc.getCachedStats()["app"].cpu != "2%" {
			t.Fatal("background refresh failed")
		}
	})
}

func TestEmptyStatsSampleDoesNotHammerDocker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		svc := &DockerContainers{statsCollector: func() map[string]liveStats { calls.Add(1); return nil }}
		svc.getCachedStats()
		synctest.Wait()
		for i := 0; i < 20; i++ {
			svc.getCachedStats()
		}
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("empty sample retried on every request")
		}
		time.Sleep(7 * time.Second)
		svc.getCachedStats()
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("empty sample never retried")
		}
	})
}
