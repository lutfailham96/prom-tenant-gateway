package metrics

import (
	"sync/atomic"
	"time"
)

type Metrics struct {
	RequestsTotal   atomic.Uint64
	RequestsSuccess atomic.Uint64
	RequestsFailed  atomic.Uint64
	StartTime       time.Time
}

func New() *Metrics {
	return &Metrics{
		StartTime: time.Now(),
	}
}

func (m *Metrics) IncTotal() {
	m.RequestsTotal.Add(1)
}

func (m *Metrics) IncSuccess() {
	m.RequestsSuccess.Add(1)
}

func (m *Metrics) IncFailed() {
	m.RequestsFailed.Add(1)
}

func (m *Metrics) Get() (total, success, failed uint64, uptimeSec float64) {
	total = m.RequestsTotal.Load()
	success = m.RequestsSuccess.Load()
	failed = m.RequestsFailed.Load()
	uptimeSec = time.Since(m.StartTime).Seconds()
	return
}
