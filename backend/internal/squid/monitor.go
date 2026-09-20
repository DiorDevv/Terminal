package squid

import (
	"context"
	"io"
	"os"
	"time"
)

const (
	// maxTailLine bounds how much of one unterminated line is held in memory.
	maxTailLine = 64 << 10
	// tailPoll is how often the file is checked for new data.
	tailPoll = 500 * time.Millisecond
)

// TailAccessLog streams new lines appended to the access log until ctx is
// cancelled. It starts at the end of the file so only fresh traffic is sent.
//
// It survives log rotation: when the path starts pointing at a different file
// (squid -k rotate, logrotate) the old file is drained first and the new one is
// then read from its beginning, so no line is lost or repeated. A line is only
// sent once complete, so a half-written line is never split in two.
//
// It returns an error only when the file cannot be opened at the start;
// otherwise it runs until ctx ends.
func TailAccessLog(ctx context.Context, path string, lines chan<- string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { f.Close() }()

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	var pending []byte // an unterminated line
	buf := make([]byte, 32<<10)

	send := func(line string) bool {
		select {
		case lines <- line:
			return true
		case <-ctx.Done():
			return false
		}
	}

	// drain reads f to its current end, sending every complete line.
	drain := func() bool {
		for {
			n, err := f.Read(buf)
			for _, b := range buf[:n] {
				if b == '\n' {
					if !send(string(pending) + "\n") {
						return false
					}
					pending = pending[:0]
					continue
				}
				if len(pending) < maxTailLine {
					pending = append(pending, b)
				}
			}
			if err != nil || n == 0 {
				return true
			}
		}
	}

	ticker := time.NewTicker(tailPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		// Look at the path first: whatever is left in the old file after this
		// check is drained below, so nothing written before the switch is lost.
		cur, curErr := os.Stat(path)
		old, oldErr := f.Stat()
		switched := curErr == nil && oldErr == nil && !os.SameFile(old, cur)

		if !drain() {
			return nil
		}

		switch {
		case switched:
			nf, err := os.Open(path)
			if err != nil {
				continue // the new file is not readable yet; try again next tick
			}
			f.Close()
			f = nf
			if len(pending) > 0 { // the old file ended without a newline
				if !send(string(pending) + "\n") {
					return nil
				}
				pending = pending[:0]
			}
		default:
			// Truncated in place (copytruncate): the file is now shorter than
			// what was already read, so start again from the top. Compared after
			// the drain, against the file itself, so growth is never mistaken
			// for truncation.
			if fi, err := f.Stat(); err == nil && fi.Size() < offset(f) {
				f.Seek(0, io.SeekStart)
				pending = pending[:0]
			}
		}
	}
}

// offset returns the current read position of f, or 0 if it cannot be told.
func offset(f *os.File) int64 {
	pos, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0
	}
	return pos
}
