// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package stats

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/DataDog/datadog-agent/pkg/trace/pb"
	"github.com/DataDog/datadog-agent/pkg/trace/traceutil"

	"github.com/stretchr/testify/assert"
)

var (
	testBucketInterval = (2 * time.Second).Nanoseconds()
)

func NewTestConcentrator() *Concentrator {
	statsChan := make(chan []pb.ClientStatsBucket)
	return NewConcentrator(testBucketInterval, statsChan)
}

// getTsInBucket gives a timestamp in ns which is `offset` buckets late
func getTsInBucket(alignedNow int64, bsize int64, offset int64) int64 {
	return alignedNow - offset*bsize + rand.Int63n(bsize)
}

// testSpan avoids typo and inconsistency in test spans (typical pitfall: duration, start time,
// and end time are aligned, and end time is the one that needs to be aligned
func testSpan(spanID uint64, parentID uint64, duration, offset int64, service, resource string, err int32) *pb.Span {
	now := time.Now().UnixNano()
	alignedNow := now - now%testBucketInterval

	return &pb.Span{
		SpanID:   spanID,
		ParentID: parentID,
		Duration: duration,
		Start:    getTsInBucket(alignedNow, testBucketInterval, offset) - duration,
		Service:  service,
		Name:     "query",
		Resource: resource,
		Error:    err,
		Type:     "db",
	}
}

// newMeasuredSpan is a function that can make measured spans as test fixtures.
func newMeasuredSpan(spanID uint64, parentID uint64, duration, offset int64, name, service, resource string, err int32) *pb.Span {
	now := time.Now().UnixNano()
	alignedNow := now - now%testBucketInterval

	return &pb.Span{
		SpanID:   spanID,
		ParentID: parentID,
		Duration: duration,
		Start:    getTsInBucket(alignedNow, testBucketInterval, offset) - duration,
		Service:  service,
		Name:     name,
		Resource: resource,
		Error:    err,
		Type:     "db",
		Metrics:  map[string]float64{"_dd.measured": 1},
	}
}

// countValsEq is a test utility function to assert expected == actual for count aggregations.
func countValsEq(t *testing.T, expected []pb.ClientGroupedStats, actual []pb.ClientGroupedStats) {
	expectedM := make(map[string]pb.ClientGroupedStats)
	actualM := make(map[string]pb.ClientGroupedStats)
	for _, e := range expected {
		e.ErrorSummary = nil
		e.OkSummary = nil
		expectedM[e.Service+e.Resource] = e
	}
	for _, a := range actual {
		a.ErrorSummary = nil
		a.OkSummary = nil
		actualM[a.Service+a.Resource] = a
	}
	assert := assert.New(t)
	assert.Equal(len(expectedM), len(actualM))
	for k, e := range expectedM {
		a := actualM[k]
		assert.Equal(e, a)
	}
}

// TestConcentratorOldestTs tests that the Agent doesn't report time buckets from a
// time before its start
func TestConcentratorOldestTs(t *testing.T) {
	assert := assert.New(t)
	now := time.Now().UnixNano()

	// Build that simply have spans spread over time windows.
	trace := pb.Trace{
		testSpan(1, 0, 50, 5, "A1", "resource1", 0),
		testSpan(1, 0, 40, 4, "A1", "resource1", 0),
		testSpan(1, 0, 30, 3, "A1", "resource1", 0),
		testSpan(1, 0, 20, 2, "A1", "resource1", 0),
		testSpan(1, 0, 10, 1, "A1", "resource1", 0),
		testSpan(1, 0, 1, 0, "A1", "resource1", 0),
	}

	traceutil.ComputeTopLevel(trace)
	wt := NewWeightedTrace(trace, traceutil.GetRoot(trace))

	testTrace := &Input{
		Env:   "none",
		Trace: wt,
	}

	t.Run("cold", func(t *testing.T) {
		// Running cold, all spans in the past should end up in the current time bucket.
		flushTime := now
		c := NewTestConcentrator()
		c.addNow(testTrace)

		for i := 0; i < c.bufferLen; i++ {
			stats := c.flushNow(flushTime)
			if !assert.Equal(0, len(stats), "We should get exactly 0 Bucket") {
				t.FailNow()
			}
			flushTime += testBucketInterval
		}

		stats := c.flushNow(flushTime)

		if !assert.Equal(1, len(stats), "We should get exactly 1 Bucket") {
			t.FailNow()
		}

		// First oldest bucket aggregates old past time buckets, so each count
		// should be an aggregated total across the spans.
		expected := []pb.ClientGroupedStats{
			{
				Service: "A1",
				Resource: "resource1",
				Type: "db",
				DBType: "db",
				Name: "query",
				Duration: 151,
				Hits: 6,
				Errors: 0,
			},
		}
		countValsEq(t, expected, stats[0].Stats)
	})

	t.Run("hot", func(t *testing.T) {
		flushTime := now
		c := NewTestConcentrator()
		c.oldestTs = alignTs(now, c.bsize) - int64(c.bufferLen-1)*c.bsize
		c.addNow(testTrace)

		for i := 0; i < c.bufferLen-1; i++ {
			stats := c.flushNow(flushTime)
			if !assert.Equal(0, len(stats), "We should get exactly 0 Bucket") {
				t.FailNow()
			}
			flushTime += testBucketInterval
		}

		stats := c.flushNow(flushTime)
		if !assert.Equal(1, len(stats), "We should get exactly 1 Bucket") {
			t.FailNow()
		}
		flushTime += testBucketInterval

		// First oldest bucket aggregates, it should have it all except the
		// last four spans that have offset of 0.
		expected := []pb.ClientGroupedStats{
			{
				Service: "A1",
				Resource: "resource1",
				Type: "db",
				DBType: "db",
				Name: "query",
				Duration: 150,
				Hits: 5,
				Errors: 0,
			},
		}
		countValsEq(t, expected, stats[0].Stats)

		stats = c.flushNow(flushTime)
		if !assert.Equal(1, len(stats), "We should get exactly 1 Bucket") {
			t.FailNow()
		}

		// Stats of the last four spans.
		expected = []pb.ClientGroupedStats{
			{
				Service: "A1",
				Resource: "resource1",
				Type: "db",
				DBType: "db",
				Name: "query",
				Duration: 1,
				Hits: 1,
				Errors: 0,
			},
		}
		countValsEq(t, expected, stats[0].Stats)
	})
}

// TestConcentratorStatsTotals tests that the total stats are correct, independently of the
// time bucket they end up.
func TestConcentratorStatsTotals(t *testing.T) {
	assert := assert.New(t)
	c := NewTestConcentrator()

	now := time.Now().UnixNano()
	alignedNow := alignTs(now, c.bsize)

	// update oldestTs as it running for quite some time, to avoid the fact that at startup
	// it only allows recent stats.
	c.oldestTs = alignedNow - int64(c.bufferLen)*c.bsize

	// Build that simply have spans spread over time windows.
	trace := pb.Trace{
		testSpan(1, 0, 50, 5, "A1", "resource1", 0),
		testSpan(1, 0, 40, 4, "A1", "resource1", 0),
		testSpan(1, 0, 30, 3, "A1", "resource1", 0),
		testSpan(1, 0, 20, 2, "A1", "resource1", 0),
		testSpan(1, 0, 10, 1, "A1", "resource1", 0),
		testSpan(1, 0, 1, 0, "A1", "resource1", 0),
	}

	traceutil.ComputeTopLevel(trace)
	wt := NewWeightedTrace(trace, traceutil.GetRoot(trace))

	t.Run("ok", func(t *testing.T) {
		testTrace := &Input{
			Env:   "none",
			Trace: wt,
		}
		c.addNow(testTrace)

		var duration uint64
		var hits uint64
		var errors uint64

		flushTime := now
		for i := 0; i <= c.bufferLen; i++ {
			stats := c.flushNow(flushTime)

			if len(stats) == 0 {
				continue
			}

			for _, b := range stats[0].Stats {
				duration += b.Duration
				hits += b.Hits
				errors += b.Errors
			}
			flushTime += c.bsize
		}

		assert.Equal(duration, uint64(50+40+30+20+10+1), "Wrong value for total duration %d", duration)
		assert.Equal(hits, uint64(len(trace)), "Wrong value for total hits %d", hits)
		assert.Equal(errors, uint64(0), "Wrong value for total errors %d", errors)
	})
}

// TestConcentratorStatsCounts tests exhaustively each stats bucket, over multiple time buckets.
func TestConcentratorStatsCounts(t *testing.T) {
	assert := assert.New(t)
	c := NewTestConcentrator()

	now := time.Now().UnixNano()
	alignedNow := alignTs(now, c.bsize)

	// update oldestTs as it running for quite some time, to avoid the fact that at startup
	// it only allows recent stats.
	c.oldestTs = alignedNow - int64(c.bufferLen)*c.bsize

	// Build a trace with stats which should cover 3 time buckets.
	trace := pb.Trace{
		// more than 2 buckets old, should be added to the 2 bucket-old, first flush.
		testSpan(1, 0, 111, 10, "A1", "resource1", 0),
		testSpan(1, 0, 222, 3, "A1", "resource1", 0),
		// 2 buckets old, part of the first flush
		testSpan(1, 0, 24, 2, "A1", "resource1", 0),
		testSpan(2, 0, 12, 2, "A1", "resource1", 2),
		testSpan(3, 0, 40, 2, "A2", "resource2", 2),
		testSpan(4, 0, 300000000000, 2, "A2", "resource2", 2), // 5 minutes trace
		testSpan(5, 0, 30, 2, "A2", "resourcefoo", 0),
		// 1 bucket old, part of the second flush
		testSpan(6, 0, 24, 1, "A1", "resource2", 0),
		testSpan(7, 0, 12, 1, "A1", "resource1", 2),
		testSpan(8, 0, 40, 1, "A2", "resource1", 2),
		testSpan(9, 0, 30, 1, "A2", "resource2", 2),
		testSpan(10, 0, 3600000000000, 1, "A2", "resourcefoo", 0), // 1 hour trace
		// present data, part of the third flush
		testSpan(6, 0, 24, 0, "A1", "resource2", 0),
	}

	expectedCountValByKeyByTime := make(map[int64][]pb.ClientGroupedStats)
	// 2-bucket old flush
	expectedCountValByKeyByTime[alignedNow-2*testBucketInterval] = []pb.ClientGroupedStats{
		{
			Service: "A1",
			Resource: "resource1",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 369,
			Hits: 4,
			Errors: 1,
		},
		{
			Service: "A2",
			Resource: "resource2",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 300000000040,
			Hits: 2,
			Errors: 2,
		},
		{
			Service: "A2",
			Resource: "resourcefoo",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 30,
			Hits: 1,
			Errors: 0,
		},
	}
	// 1-bucket old flush
	expectedCountValByKeyByTime[alignedNow-testBucketInterval] = []pb.ClientGroupedStats{
		{
			Service: "A1",
			Resource: "resource1",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 12,
			Hits: 1,
			Errors: 1,
		},
		{
			Service: "A1",
			Resource: "resource2",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 24,
			Hits: 1,
			Errors: 0,
		},
		{
			Service: "A2",
			Resource: "resource1",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 40,
			Hits: 1,
			Errors: 1,
		},
		{
			Service: "A2",
			Resource: "resource2",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 30,
			Hits: 1,
			Errors: 1,
		},
		{
			Service: "A2",
			Resource: "resourcefoo",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 3600000000000,
			Hits: 1,
			Errors: 0,
		},
	}
	// last bucket to be flushed
	expectedCountValByKeyByTime[alignedNow] = []pb.ClientGroupedStats{
		{
			Service: "A1",
			Resource: "resource2",
			Type: "db",
			DBType: "db",
			Name: "query",
			Duration: 24,
			Hits: 1,
			Errors: 0,
		},
	}
	expectedCountValByKeyByTime[alignedNow+testBucketInterval] = []pb.ClientGroupedStats{}

	traceutil.ComputeTopLevel(trace)
	wt := NewWeightedTrace(trace, traceutil.GetRoot(trace))

	testTrace := &Input{
		Env:   "none",
		Trace: wt,
	}
	c.addNow(testTrace)

	// flush every testBucketInterval
	flushTime := now
	for i := 0; i <= c.bufferLen+2; i++ {
		t.Run(fmt.Sprintf("flush-%d", i), func(t *testing.T) {
			stats := c.flushNow(flushTime)

			expectedFlushedTs := alignTs(flushTime, c.bsize) - int64(c.bufferLen)*testBucketInterval
			if len(expectedCountValByKeyByTime[expectedFlushedTs]) == 0 {
				// That's a flush for which we expect no data
				return
			}
			if !assert.Equal(1, len(stats), "We should get exactly 1 Bucket") {
				t.FailNow()
			}
			assert.Equal(uint64(expectedFlushedTs), stats[0].Start)
			expectedCountValByKey := expectedCountValByKeyByTime[expectedFlushedTs]
			countValsEq(t, expectedCountValByKey, stats[0].Stats)

			// Flushing again at the same time should return nothing
			stats = c.flushNow(flushTime)
			if !assert.Equal(0, len(stats), "Second flush of the same time should be empty") {
				t.FailNow()
			}

		})
		flushTime += c.bsize
	}
}

// TestConcentratorAdd tests the count aggregation behavior of addNow.
// func TestConcentratorAdd(t *testing.T) {
// 	now := time.Now().UnixNano()
// 	for name, test := range map[string]struct {
// 		in  pb.Trace
// 		out []pb.ClientGroupedStats
// 	}{
// 		// case of existing behavior
// 		"top": {
// 			pb.Trace{
// 				testSpan(1, 0, 50, 5, "A1", "resource1", 0),
// 				testSpan(2, 1, 40, 4, "A1", "resource1", 1),
// 			},
// 			[]pb.ClientGroupedStats{
// 				{
// 					Service: "A1",
// 					Resource: "resource1",
// 					Duration: 50,
// 					Hits: 1,
// 					Errors: 0,
// 				},
// 			},
// 		},
// 		// mixed = first span is both top-level _and_ measured
// 		"mixed": {
// 			pb.Trace{
// 				newMeasuredSpan(1, 0, 50, 5, "http.request", "A1", "resource1", 0),
// 				testSpan(2, 1, 40, 4, "A1", "resource1", 1),
// 			},
// 			[]pb.ClientGroupedStats{
// 				{
// 					Service: "A1",
// 					Resource: "resource1",
// 					Duration: 50,
// 					Hits: 1,
// 					Errors: 0,
// 				},
// 			map[string]float64{
// 				"http.request|duration|env:none,resource:resource1,service:A1":                                           50,
// 				"http.request|hits|env:none,resource:resource1,service:A1":                                               1,
// 				"http.request|errors|env:none,resource:resource1,service:A1":                                             0,
// 				"http.request|_sublayers.duration.by_service|env:none,resource:resource1,service:A1,sublayer_service:A1": 90,
// 				"http.request|_sublayers.duration.by_type|env:none,resource:resource1,service:A1,sublayer_type:db":       90,
// 				"http.request|_sublayers.span_count|env:none,resource:resource1,service:A1,:":                            2,
// 			},
// 		},
// 		// distinct top-level span and measured span
// 		// the top-level span and measured span get sublayer metrics
// 		"distinct": {
// 			pb.Trace{
// 				testSpan(1, 0, 50, 5, "A1", "resource1", 0),
// 				newMeasuredSpan(2, 1, 40, 4, "custom_query_op", "A1", "resource1", 1),
// 				testSpan(3, 2, 50, 5, "A1", "resource1", 0),
// 			},
// 			map[string]float64{
// 				"query|duration|env:none,resource:resource1,service:A1":                                                     50,
// 				"query|hits|env:none,resource:resource1,service:A1":                                                         1,
// 				"query|errors|env:none,resource:resource1,service:A1":                                                       0,
// 				"custom_query_op|duration|env:none,resource:resource1,service:A1":                                           40,
// 				"custom_query_op|hits|env:none,resource:resource1,service:A1":                                               1,
// 				"custom_query_op|errors|env:none,resource:resource1,service:A1":                                             1,
// 			},
// 		},
// 	} {
// 		t.Run(name, func(*testing.T) {
// 			traceutil.ComputeTopLevel(test.in)
// 			wt := NewWeightedTrace(test.in, traceutil.GetRoot(test.in))
// 			testTrace := &Input{
// 				Env:   "none",
// 				Trace: wt,
// 			}
// 			c := NewTestConcentrator()
// 			c.addNow(testTrace)
// 			stats := c.flushNow(now + (int64(c.bufferLen) * testBucketInterval))
// 			countValsEq(t, test.out, stats[0].Counts)
// 		})
// 	}
// }
