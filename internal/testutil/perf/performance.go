package perf

import (
	"runtime"
	"sort"
	"testing"
	"time"
)

// PerformanceSample measures warmed, sequential requests. Allocation is total
// Go heap allocation during those requests, never resident memory.
type PerformanceSample struct {
	Name            string  `json:"name"`
	Samples         int     `json:"samples"`
	MedianMicros    float64 `json:"medianMicros"`
	P95Micros       float64 `json:"p95Micros"`
	BytesPerRequest uint64  `json:"bytesPerRequest"`
}

func MeasureRequests(t testing.TB, name string, count int, request func() error) PerformanceSample {
	t.Helper()
	for i := 0; i < 3; i++ {
		if err := request(); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	times := make([]time.Duration, count)
	var start, end runtime.MemStats
	runtime.ReadMemStats(&start)
	for i := range times {
		at := time.Now()
		if err := request(); err != nil {
			t.Fatal(err)
		}
		times[i] = time.Since(at)
	}
	runtime.ReadMemStats(&end)
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	return PerformanceSample{Name: name, Samples: count, MedianMicros: float64(times[count/2]) / 1000, P95Micros: float64(times[(count*95+99)/100-1]) / 1000, BytesPerRequest: (end.TotalAlloc - start.TotalAlloc) / uint64(count)}
}
