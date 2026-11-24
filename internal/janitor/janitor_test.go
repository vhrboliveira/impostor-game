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
}

type fakeStore struct {
	calls   chan call
	deleted int64
	err     error
}

func newFakeStore() *fakeStore {
	return &fakeStore{calls: make(chan call, 16)}
}

func (f *fakeStore) DeleteExpired(_ context.Context, now time.Time, emptyTTL time.Duration) (int64, error) {
	f.calls <- call{now: now, emptyTTL: emptyTTL}
	return f.deleted, f.err
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
