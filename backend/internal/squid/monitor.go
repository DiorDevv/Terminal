package squid

import (
	"bufio"
	"context"
	"io"
	"os"
	"time"
)

// TailAccessLog streams new lines appended to the access log until ctx is
// cancelled. It starts at the end of the file so only fresh traffic is sent.
func TailAccessLog(ctx context.Context, path string, lines chan<- string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	reader := bufio.NewReader(f)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for {
				line, err := reader.ReadString('\n')
				if line != "" {
					select {
					case lines <- line:
					case <-ctx.Done():
						return nil
					}
				}
				if err != nil {
					break
				}
			}
		}
	}
}
