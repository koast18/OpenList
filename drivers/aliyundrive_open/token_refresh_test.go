package aliyundrive_open

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/pkg/cron"
)

// TestTokenRefreshCronFires checks that the periodic refresh actually runs, so a
// storage that is never touched still exchanges its token well inside the 90-day
// validity window.
func TestTokenRefreshCronFires(t *testing.T) {
	var calls int32
	d := &AliyundriveOpen{}
	d.refreshFn = func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}
	// Substitute a fast ticker: the production interval is a week, which a test
	// cannot wait for. startTokenRefresh only builds the cron, so drive it here.
	d.cron = cron.NewCron(10 * time.Millisecond)
	d.cron.Do(func() {
		if err := d.refreshForCron(); err != nil {
			t.Errorf("refreshForCron: %v", err)
		}
	})
	defer d.stopTokenRefresh()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&calls) >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("periodic refresh fired %d time(s), want at least 2", atomic.LoadInt32(&calls))
}

// TestStartTokenRefreshSkipsReferencedDriver makes sure only the storage that
// owns the credentials exchanges them. A Crypt mount reuses its parent's driver
// through InitReference; a timer there would refresh on somebody else's behalf.
func TestStartTokenRefreshSkipsReferencedDriver(t *testing.T) {
	parent := &AliyundriveOpen{}
	child := &AliyundriveOpen{ref: parent}
	child.startTokenRefresh()
	if child.cron != nil {
		t.Fatal("a driver that only references another one must not start a timer")
	}

	// The owning storage does get one, and Drop cleans it up.
	parent.startTokenRefresh()
	if parent.cron == nil {
		t.Fatal("the owning driver should start a timer")
	}
	parent.stopTokenRefresh()
	if parent.cron != nil {
		t.Fatal("stopTokenRefresh should clear the timer")
	}
}

// TestDropStopsTokenRefresh covers the leak that would otherwise keep a deleted
// storage refreshing its token forever.
func TestDropStopsTokenRefresh(t *testing.T) {
	var calls int32
	d := &AliyundriveOpen{}
	d.refreshFn = func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}
	d.limiter = getLimiterForUser(globalLimiterUserID)
	d.cron = cron.NewCron(5 * time.Millisecond)
	d.cron.Do(func() { _ = d.refreshForCron() })

	if err := d.Drop(context.Background()); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if d.cron != nil {
		t.Fatal("Drop should stop the timer")
	}

	after := atomic.LoadInt32(&calls)
	time.Sleep(60 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != after {
		t.Fatalf("refresh kept firing after Drop: %d -> %d", after, got)
	}
}
