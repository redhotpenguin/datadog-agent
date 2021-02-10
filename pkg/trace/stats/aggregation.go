package stats

import (
	"strconv"
	"strings"

	"github.com/DataDog/datadog-agent/pkg/trace/pb"
)

const (
	tagHostname   = "_dd.hostname"
	tagStatusCode = "http.status_code"
	tagVersion    = "version"
	tagOrigin     = "_dd.origin"
)

// Aggregation contains all the dimension on which we aggregate statistics
// when adding or removing fields to Aggregation the methods ToTagSet, KeyLen and
// WriteKey should always be updated accordingly
type Aggregation struct {
	Env        string
	Resource   string
	Service    string
	Type       string
	DBType     string
	Hostname   string
	StatusCode uint32
	Version    string
	Synthetics bool
}

// NewAggregationFromSpan creates a new aggregation from the provided span and env
func NewAggregationFromSpan(s *pb.Span, env string) Aggregation {
	synthetics := strings.HasPrefix(s.Meta[tagOrigin], "synthetics")
	statusCode, err := strconv.Atoi(s.Meta[tagStatusCode])
	if err != nil {
		statusCode = 0
	}

	return Aggregation{
		Env:        env,
		Resource:   s.Resource,
		Service:    s.Service,
		Type: s.Type,
		// todo[piochelepiotr] What is DBType vs type?
		DBType: s.Type,
		Hostname:   s.Meta[tagHostname],
		StatusCode: uint32(statusCode),
		Version:    s.Meta[tagVersion],
		Synthetics: synthetics,
	}
}
