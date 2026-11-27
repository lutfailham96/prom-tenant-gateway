package main

import (
	"fmt"
	"strconv"
)

// memoryLimitRatio is the share of the container memory limit used as the Go
// soft memory limit; the rest is headroom for non-heap memory (stacks,
// runtime metadata) before the container is OOM-killed.
const memoryLimitRatio = 0.9

// memoryLimit derives the Go soft memory limit from MEMORY_LIMIT_BYTES (the
// container memory limit in bytes, injected by the Kubernetes Downward API
// from resources.limits.memory). It returns ok=false when GOMEMLIMIT is set
// (the Go runtime already applied it and it takes precedence) or
// MEMORY_LIMIT_BYTES is unset.
func memoryLimit(getenv func(string) string) (limit int64, ok bool, err error) {
	if getenv("GOMEMLIMIT") != "" {
		return 0, false, nil
	}
	v := getenv("MEMORY_LIMIT_BYTES")
	if v == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, false, fmt.Errorf("MEMORY_LIMIT_BYTES=%q: must be a positive number of bytes", v)
	}
	return int64(float64(n) * memoryLimitRatio), true, nil
}
