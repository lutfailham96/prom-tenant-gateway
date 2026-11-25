package metrics_test

import (
	"testing"

	"github.com/lutfailham96/prom-tenant-gateway/internal/metrics"
	"github.com/stretchr/testify/assert"
)

func TestMetrics_Increment(t *testing.T) {
	m := metrics.New()

	total, success, failed, uptime := m.Get()
	assert.Equal(t, [3]uint64{0, 0, 0}, [3]uint64{total, success, failed})
	assert.GreaterOrEqual(t, uptime, 0.0)

	m.IncTotal()
	m.IncTotal()
	m.IncSuccess()
	m.IncFailed()

	total, success, failed, _ = m.Get()
	assert.Equal(t, [3]uint64{2, 1, 1}, [3]uint64{total, success, failed})
}
