package metrics

import (
	"sync/atomic"
	"time"
)

type Metrics struct {
	RequestsTotal   uint64
	RequestsSuccess uint64
	RequestsFailed  uint64
	StartTime       time.Time
}

func New() *Metrics {
	return &Metrics{
		StartTime: time.Now(),
	}
}

func (m *Metrics) IncTotal() {
	atomic.AddUint64(&m.RequestsTotal, 1)
}

func (m *Metrics) IncSuccess() {
	atomic.AddUint64(&m.RequestsSuccess, 1)
}

func (m *Metrics) IncFailed() {
	atomic.AddUint64(&m.RequestsFailed, 1)
}

func (m *Metrics) Get() (total, success, failed uint64, uptimeSec float64) {
	total = atomic.LoadUint64(&m.RequestsTotal)
	success = atomic.LoadUint64(&m.RequestsSuccess)
	failed = atomic.LoadUint64(&m.RequestsFailed)
	uptimeSec = time.Since(m.StartTime).Seconds()
	return
}