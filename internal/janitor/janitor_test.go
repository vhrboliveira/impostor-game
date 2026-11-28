package janitor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type call struct {
	now      time.Time
	emptyTTL time.Duration
	limit    int
}

type fakeStore struct {
	calls chan call
	// deleted is what each call returns, in order; the last value repeats.
	deleted []int64
	err     error
	// block makes the call wait for its context.
	block bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{calls: make(chan call, 16)}
}

func (f *fakeStore) DeleteExpired(ctx context.Context, now time.Time, emptyTTL time.Duration, limit int) (int64, error) {
	f.calls <- call{now: now, emptyTTL: emptyTTL, limit: limit}
	if f.block {
		<-ctx.Done()
		return 0, ctx.Err()
	}
	var n int64
	if len(f.deleted) > 0 {
		n = f.deleted[0]
		if len(f.deleted) > 1 {
			f.deleted = f.deleted[1:]
		}
	}
	return n, f.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunOnce_PassesTheClockAndTheEmptyTTL(t *testing.T) {
	store := newFakeStore()
	fixed := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	j := New(store, discardLogger(), time.Minute, time.Hour)
	j.now = func() time.Time { return fixed }

	require.NoError(t, j.RunOnce(context.Background()))

	got := <-store.calls
	assert.Equal(t, fixed, got.now)
	assert.Equal(t, time.Hour, got.emptyTTL)
}

func TestRunOnce_WrapsStoreErrors(t *testing.T) {
	store := newFakeStore()
	store.err = errors.New("boom")
	j := New(store, discardLogger(), time.Minute, time.Hour)

	err := j.RunOnce(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, store.err)
}

func TestRun_CleansImmediatelyAndOnEveryTickUntilCanceled(t *testing.T) {
	store := newFakeStore()
	j := New(store, discardLogger(), 5*time.Millisecond, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		j.Run(ctx)
		close(done)
	}()

	for i := range 3 {
		select {
		case <-store.calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("cleanup %d did not run in time", i+1)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after the context was canceled")
	}
}

// Deleting thousands of rooms (with their cascades) in one statement holds row locks for the
// whole batch: the janitor deletes in bounded batches until a batch comes back short.
func TestRunOnce_DeletesInBatches(t *testing.T) {
	store := newFakeStore()
	store.deleted = []int64{DefaultBatchSize, DefaultBatchSize, 3}
	j := New(store, discardLogger(), time.Minute, time.Hour)

	require.NoError(t, j.RunOnce(context.Background()))

	require.Len(t, store.calls, 3)
	for range 3 {
		assert.Equal(t, DefaultBatchSize, (<-store.calls).limit)
	}
}

// A hung database must not stall the cleanup forever: each run has its own deadline.
func TestRunOnce_HasADeadline(t *testing.T) {
	store := newFakeStore()
	store.block = true
	j := New(store, discardLogger(), time.Minute, time.Hour)
	j.runTimeout = 20 * time.Millisecond

	done := make(chan error, 1)
	go func() { done <- j.RunOnce(context.Background()) }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnce did not give up")
	}
}
