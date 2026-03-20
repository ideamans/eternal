package watcher

import (
	"log"
	"os"
	"time"
)

// WatchBinary polls the binary at the given path and calls onChange when
// the file's modification time or size changes.
func WatchBinary(path string, onChange func()) {
	info, err := os.Stat(path)
	if err != nil {
		log.Printf("watcher: cannot stat %s: %v", path, err)
		return
	}

	lastMod := info.ModTime()
	lastSize := info.Size()

	for {
		time.Sleep(2 * time.Second)

		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		if info.ModTime() != lastMod || info.Size() != lastSize {
			log.Printf("watcher: binary changed (%s), triggering restart", path)
			onChange()
			return
		}
	}
}
