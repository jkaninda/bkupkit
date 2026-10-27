/*
MIT License

Copyright (c) 2026 Jonas Kaninda

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package index

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	gostorage "github.com/jkaninda/go-storage"
)

// LockTTL is how long a lock is valid; a crashed holder's lock expires.
var LockTTL = 2 * time.Minute

// LockWait is how long withLock waits for a busy lock.
var LockWait = 3 * time.Minute

type lockFile struct {
	Owner   string    `json:"owner"`
	Expires time.Time `json:"expires"`
}

// lockKey must differ from "<index>.lock", which go-storage's local backend
// uses internally for conditional writes.
func (ix *Index) lockKey() string { return ix.key() + ".writer.lock" }

// withLock runs fn while holding <index>.writer.lock. It uses PutIf(IfNoneMatch)
// where supported (atomic) and a write-then-verify best effort otherwise.
func (ix *Index) withLock(ctx context.Context, fn func() error) error {
	deadline := ix.Now().Add(LockWait)
	for attempt := 0; ; attempt++ {
		ok, err := ix.tryLock(ctx)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		if ix.Now().After(deadline) {
			return fmt.Errorf("index %s is locked by another process (delete %s if it is stale)", ix.Name, ix.lockKey())
		}
		if err := sleep(ctx, backoff(min(attempt, 5))); err != nil {
			return err
		}
	}
	defer func() {
		// Release with a fresh context so cancellation still frees the lock.
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := ix.Loc.Remove(rctx, ix.lockKey()); err != nil && !errors.Is(err, gostorage.ErrNotFound) {
			ix.Log.Warn("could not release index lock", "lock", ix.lockKey(), "error", err)
		}
	}()
	return fn()
}

func (ix *Index) tryLock(ctx context.Context) (bool, error) {
	body, _ := json.Marshal(lockFile{Owner: ix.Owner, Expires: ix.Now().Add(LockTTL).UTC()})
	_, err := ix.Loc.PutIf(ctx, ix.lockKey(), bytes.NewReader(body), gostorage.Condition{IfNoneMatch: true}, putOpts)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, gostorage.ErrPreconditionFailed):
		return false, ix.breakExpired(ctx)
	case !errors.Is(err, gostorage.ErrConditionNotSupported):
		return false, fmt.Errorf("lock index: %w", err)
	}
	// Best effort (FTP): only write when absent or expired, then verify ownership.
	cur, err := ix.readLock(ctx)
	if err != nil && !errors.Is(err, gostorage.ErrNotFound) {
		return false, err
	}
	if cur != nil && ix.Now().Before(cur.Expires) {
		return false, nil
	}
	if _, err := ix.Loc.Put(ctx, ix.lockKey(), bytes.NewReader(body), putOpts); err != nil {
		return false, fmt.Errorf("lock index: %w", err)
	}
	if err := sleep(ctx, 300*time.Millisecond); err != nil {
		return false, err
	}
	cur, err = ix.readLock(ctx)
	if err != nil {
		return false, nil
	}
	return cur.Owner == ix.Owner, nil
}

func (ix *Index) readLock(ctx context.Context) (*lockFile, error) {
	rc, _, err := ix.Loc.Get(ctx, ix.lockKey())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	l := &lockFile{}
	if err := json.NewDecoder(io.LimitReader(rc, 4096)).Decode(l); err != nil {
		// Unreadable lock: treat as expired.
		return &lockFile{}, nil
	}
	return l, nil
}

func (ix *Index) breakExpired(ctx context.Context) error {
	cur, err := ix.readLock(ctx)
	if errors.Is(err, gostorage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ix.Now().After(cur.Expires) {
		ix.Log.Warn("removing expired index lock", "lock", ix.lockKey(), "owner", cur.Owner)
		if err := ix.Loc.Remove(ctx, ix.lockKey()); err != nil && !errors.Is(err, gostorage.ErrNotFound) {
			return err
		}
	}
	return nil
}
