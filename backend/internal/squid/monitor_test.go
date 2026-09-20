package squid

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type tail struct {
	lines  chan string
	cancel context.CancelFunc
	done   chan error
}

func startTail(t *testing.T, path string) *tail {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	tl := &tail{lines: make(chan string, 100), cancel: cancel, done: make(chan error, 1)}
	go func() { tl.done <- TailAccessLog(ctx, path, tl.lines) }()
	t.Cleanup(cancel)
	return tl
}

func (tl *tail) next(t *testing.T) string {
	t.Helper()
	select {
	case l := <-tl.lines:
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a line")
		return ""
	}
}

func (tl *tail) expectNothing(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case l := <-tl.lines:
		t.Fatalf("unexpected line %q", l)
	case <-time.After(wait):
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestTailSendsOnlyNewLinesAndOnlyWholeOnes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "old line\n")
	tl := startTail(t, path)
	time.Sleep(700 * time.Millisecond) // let it reach the end of the file

	appendTo(t, path, "first\n")
	if got := tl.next(t); got != "first\n" {
		t.Fatalf("got %q, want the first new line only (not the old one)", got)
	}

	// A line written in two pieces must arrive as one line.
	appendTo(t, path, "half of a li")
	tl.expectNothing(t, 900*time.Millisecond)
	appendTo(t, path, "ne\nsecond\n")
	if got := tl.next(t); got != "half of a line\n" {
		t.Fatalf("a partial line was split: %q", got)
	}
	if got := tl.next(t); got != "second\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTailFollowsRotationWithoutLosingOrRepeatingLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	appendTo(t, path, "")
	tl := startTail(t, path)
	time.Sleep(700 * time.Millisecond)

	appendTo(t, path, "before rotation\n")
	// Rotate straight away: the line above may still be unread in the old file.
	if err := os.Rename(path, path+".0"); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, "after rotation 1\nafter rotation 2\n")

	for _, want := range []string{"before rotation\n", "after rotation 1\n", "after rotation 2\n"} {
		if got := tl.next(t); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	appendTo(t, path, "later\n")
	if got := tl.next(t); got != "later\n" {
		t.Fatalf("the new file must keep being followed: %q", got)
	}
	tl.expectNothing(t, 900*time.Millisecond) // nothing repeated
}

func TestTailSurvivesTruncationInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	tl := startTail(t, path)
	time.Sleep(700 * time.Millisecond)

	appendTo(t, path, "a long first line to make the file big\n")
	tl.next(t)
	if err := os.Truncate(path, 0); err != nil { // copytruncate style
		t.Fatal(err)
	}
	appendTo(t, path, "new\n")
	if got := tl.next(t); got != "new\n" {
		t.Fatalf("after truncation got %q", got)
	}
	tl.expectNothing(t, 900*time.Millisecond)
}

func TestTailReportsAnUnreadableFileAndStopsWhenCancelled(t *testing.T) {
	// A missing file at the start is an error the caller can show.
	ctx := context.Background()
	if err := TailAccessLog(ctx, filepath.Join(t.TempDir(), "missing.log"), make(chan string, 1)); err == nil {
		t.Error("a missing log file must be reported")
	}

	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	tl := startTail(t, path)
	time.Sleep(200 * time.Millisecond)
	tl.cancel()
	select {
	case err := <-tl.done:
		if err != nil {
			t.Errorf("cancelling is not an error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the tail did not stop after cancel")
	}
}

func TestTailBoundsAnUnterminatedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	appendTo(t, path, "")
	tl := startTail(t, path)
	time.Sleep(700 * time.Millisecond)

	huge := make([]byte, 3*maxTailLine)
	for i := range huge {
		huge[i] = 'x'
	}
	appendTo(t, path, string(huge)+"\n")
	if got := tl.next(t); len(got) > maxTailLine+1 {
		t.Errorf("a line without an end must not be held in full: %d bytes", len(got))
	}
}
