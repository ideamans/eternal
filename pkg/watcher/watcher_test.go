package watcher

import (
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatchBinary_DetectsChange(t *testing.T) {
	// Create a temp file to watch
	f, err := os.CreateTemp("", "et-watch-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("v1"))
	f.Close()
	defer os.Remove(f.Name())

	var called atomic.Int32
	go WatchBinary(f.Name(), func() {
		called.Add(1)
	})

	// Wait for watcher to start
	time.Sleep(500 * time.Millisecond)

	// Modify the file
	os.WriteFile(f.Name(), []byte("v2-updated"), 0644)

	// Wait for detection (polling interval is 2s)
	time.Sleep(3 * time.Second)

	if called.Load() != 1 {
		t.Errorf("onChange called %d times, want 1", called.Load())
	}
}

func TestWatchBinary_NoChangeNoCallback(t *testing.T) {
	f, err := os.CreateTemp("", "et-watch-test-*")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("stable"))
	f.Close()
	defer os.Remove(f.Name())

	var called atomic.Int32
	go WatchBinary(f.Name(), func() {
		called.Add(1)
	})

	time.Sleep(3 * time.Second)

	if called.Load() != 0 {
		t.Errorf("onChange called %d times, want 0", called.Load())
	}
}
