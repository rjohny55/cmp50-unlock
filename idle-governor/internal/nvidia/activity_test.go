package nvidia

import (
	"testing"
	"unsafe"
)

func TestNVMLSampleABILayout(t *testing.T) {
	var sample activitySample
	if unsafe.Sizeof(sample) != 16 || unsafe.Offsetof(sample.Value) != 8 {
		t.Fatal("nvmlSample_t ABI mismatch")
	}
}

func TestNVMLHistoryCatchesShortBurstsBetweenThirtySecondPolls(t *testing.T) {
	const start = uint64(100000000)
	samples := make([]activitySample, 30)
	for i := range samples {
		samples[i] = activitySample{Timestamp: start + uint64(i+1)*1000000}
	}
	samples[14].Value = 1
	peak, last, err := activityPeak(samples, start, start+30000000)
	if err != nil || peak != 1 || last != start+30000000 {
		t.Fatalf("burst missed: %d %d %v", peak, last, err)
	}
	samples[14].Value = 0
	if peak, _, err := activityPeak(samples, start, start+30000000); err != nil || peak != 0 {
		t.Fatalf("quiet interval rejected: %d %v", peak, err)
	}
}

func TestMissingStaleOrTruncatedHistoryIsNotIdle(t *testing.T) {
	const start = uint64(100000000)
	for _, samples := range [][]activitySample{nil, {{Timestamp: start + 10000000}}, {{Timestamp: start + 1000000}}, {{Timestamp: start + 1000000, Value: 101}}} {
		if _, _, err := activityPeak(samples, start, start+30000000); err == nil {
			t.Fatal("incomplete/invalid interval accepted as quiet")
		}
	}
}

func TestOverlappingHistoryRetainsOriginalBusyTimestamp(t *testing.T) {
	const start = uint64(100000000)
	samples := []activitySample{{Timestamp: start + 1000000, Value: 4}, {Timestamp: start + 2000000}, {Timestamp: start + 3000000}}
	peak, last, busy, err := activityPeakWithTime(samples, start, start+3000000)
	if err != nil || peak != 4 || last != start+3000000 || busy != start+1000000 {
		t.Fatalf("invalid activity timestamps %d %d %d %v", peak, last, busy, err)
	}
	_, _, again, err := activityPeakWithTime(samples, start+500000, start+3500000)
	if err != nil || again != busy {
		t.Fatalf("overlap fabricated fresh work %d %v", again, err)
	}
}
