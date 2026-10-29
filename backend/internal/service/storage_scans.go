package service

import (
	"encoding/json"
	"strings"
	"sync"
)

// Share concurrent work without caching completed scans or delaying mutations.
// Each scan has a Docker deadline; failures are returned to every waiter.
type sharedStorageScan[T any] struct {
	mu     sync.Mutex
	active *storageScanCall[T]
}
type storageScanCall[T any] struct {
	done  chan struct{}
	items []T
	err   error
}

func (s *sharedStorageScan[T]) get(scan func() ([]T, error)) ([]T, error) {
	s.mu.Lock()
	if call := s.active; call != nil {
		s.mu.Unlock()
		<-call.done
		return call.items, call.err
	}
	call := &storageScanCall[T]{done: make(chan struct{})}
	s.active = call
	s.mu.Unlock()
	call.items, call.err = scan()
	s.mu.Lock()
	s.active = nil
	close(call.done)
	s.mu.Unlock()
	return call.items, call.err
}

func parseVolumeSizes(output []byte) map[string]string {
	sizes := make(map[string]string)
	type volumeSize struct {
		Name       string `json:"Name"`
		VolumeName string `json:"VolumeName"`
		Size       string `json:"Size"`
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		var envelope struct {
			Volumes []volumeSize `json:"Volumes"`
		}
		if json.Unmarshal([]byte(line), &envelope) != nil {
			continue
		}
		volumes := envelope.Volumes
		if volumes == nil {
			var row volumeSize
			if json.Unmarshal([]byte(line), &row) != nil {
				continue
			}
			volumes = []volumeSize{row}
		}
		for _, volume := range volumes {
			name := volume.Name
			if name == "" {
				name = volume.VolumeName
			}
			if name != "" && volume.Size != "" {
				sizes[name] = volume.Size
			}
		}
	}
	return sizes
}
