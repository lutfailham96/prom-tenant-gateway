package ratelimit

import (
	"strconv"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTest(t *testing.T, max int, window time.Duration) (*Limiter, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	l, err := newWithClock(max, window, clock.now)
	require.NoError(t, err)
	return l, clock
}

func TestNew_RejectsNonPositiveSettings(t *testing.T) {
	for _, tc := range []struct {
		max    int
		window time.Duration
	}{{0, time.Minute}, {-1, time.Minute}, {5, 0}, {5, -time.Second}} {
		_, err := New(tc.max, tc.window)
		assert.Error(t, err, "max=%d window=%s", tc.max, tc.window)
	}

	l, err := New(5, 500*time.Millisecond)
	require.NoError(t, err)
	assert.Equal(t, 5, l.Max())
	assert.Equal(t, 500*time.Millisecond, l.Window(), "sub-second windows are kept, not rounded")
}

func TestFail_LimitsAfterMaxFailures(t *testing.T) {
	l, clock := newTest(t, 3, 10*time.Second)

	for i := 1; i <= 3; i++ {
		limited, _ := l.Fail("a")
		assert.False(t, limited, "failure %d", i)
	}

	clock.advance(4 * time.Second)
	limited, retryAfter := l.Fail("a")
	assert.True(t, limited)
	assert.Equal(t, 6*time.Second, retryAfter, "window started at the first failure")

	limited, _ = l.Fail("a")
	assert.True(t, limited, "stays limited until the window ends")
}

func TestFail_WindowReset(t *testing.T) {
	l, clock := newTest(t, 2, 10*time.Second)
	for i := 0; i < 5; i++ {
		l.Fail("a")
	}

	clock.advance(10 * time.Second)
	limited, _ := l.Fail("a")
	assert.False(t, limited, "new window")
	limited, _ = l.Fail("a")
	assert.False(t, limited)
	limited, _ = l.Fail("a")
	assert.True(t, limited)
}

func TestFail_KeysAreIndependent(t *testing.T) {
	l, _ := newTest(t, 1, time.Minute)

	l.Fail("a")
	limited, _ := l.Fail("a")
	assert.True(t, limited)

	limited, _ = l.Fail("b")
	assert.False(t, limited)
}

func TestFail_SweepsExpiredBuckets(t *testing.T) {
	l, clock := newTest(t, 5, 10*time.Second)
	for i := 0; i < 100; i++ {
		l.Fail("10.0.0." + strconv.Itoa(i))
	}
	assert.Equal(t, 100, l.Len())

	clock.advance(11 * time.Second)
	l.Fail("fresh")
	assert.Equal(t, 1, l.Len())
}

// The stored key must not alias the caller's buffer.
func TestFail_CopiesKey(t *testing.T) {
	l, _ := newTest(t, 1, time.Minute)

	// Like strings returned by fasthttp, key aliases a reusable buffer.
	buf := []byte("1.2.3.4")
	key := unsafe.String(&buf[0], len(buf))
	l.Fail(key)
	copy(buf, "9.9.9.9") // buffer reused by the next request

	limited, _ := l.Fail("1.2.3.4")
	assert.True(t, limited)
}

func TestFail_Concurrent(t *testing.T) {
	l, err := New(1000, time.Minute)
	require.NoError(t, err)

	var wg sync.WaitGroup
	var mu sync.Mutex
	limitedCount := 0
	for g := 0; g < 4; g++ { // 4 x 250 = max
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				if limited, _ := l.Fail("k"); limited {
					mu.Lock()
					limitedCount++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	assert.Zero(t, limitedCount, "exactly max failures are allowed")

	limited, _ := l.Fail("k")
	assert.True(t, limited)
}
