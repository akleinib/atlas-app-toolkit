package tracing

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/trace"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/stats"
)

var testGRPCOptions = &gRPCOptions{}

// testExporter is a test helper to capture span data
type testExporter struct {
	spans []*trace.SpanData
}

func (e *testExporter) ExportSpan(s *trace.SpanData) {
	e.spans = append(e.spans, s)
}

func TestDefaultGRPCOptions(t *testing.T) {
	expected := &gRPCOptions{
		metadataMatcher: defaultMetadataMatcher,
		maxPayloadSize:  1048576,
	}

	result := defaultGRPCOptions()
	expectedHeader, expectedBool := expected.metadataMatcher(expectedStr)
	resultHeader, resultBool := result.metadataMatcher(expectedStr)
	assert.True(t, expectedBool)
	assert.Equal(t, expectedBool, resultBool)
	assert.Equal(t, expectedHeader, resultHeader)
	assert.Equal(t, expected.maxPayloadSize, result.maxPayloadSize)
}

func TestWithMetadataAnnotation(t *testing.T) {
	option := WithMetadataAnnotation(func(ctx context.Context, stats stats.RPCStats) bool {
		return true
	})
	option(testGRPCOptions)
	assert.True(t, testGRPCOptions.spanWithMetadata(nil, nil))
}

func TestWithMetadataMatcher(t *testing.T) {
	option := WithMetadataMatcher(func(s string) (string, bool) {
		return s, true
	})
	option(testGRPCOptions)
	resultStr, ok := testGRPCOptions.metadataMatcher(expectedStr)
	assert.True(t, ok)
	assert.Equal(t, expectedStr, resultStr)
}

func TestWithGRPCPayloadAnnotation(t *testing.T) {
	option := WithGRPCPayloadAnnotation(func(ctx context.Context, rpcStats stats.RPCStats) bool {
		return true
	})
	option(testGRPCOptions)
	assert.True(t, testGRPCOptions.spanWithPayload(nil, nil))
}

func TestWithGRPCPayloadLimit(t *testing.T) {
	option := WithGRPCPayloadLimit(333)
	option(testGRPCOptions)
	assert.Equal(t, 333, testGRPCOptions.maxPayloadSize)
}

func TestNewServerHandler(t *testing.T) {
	result := NewServerHandler(func(options *gRPCOptions) {
		options.spanWithPayload = func(ctx context.Context, rpcStats stats.RPCStats) bool {
			return true
		}
	})

	matcherStr, ok := result.options.metadataMatcher(expectedStr)
	assert.True(t, ok)
	assert.Equal(t, expectedStr, matcherStr)
	assert.True(t, result.options.spanWithPayload(nil, nil))
	assert.Equal(t, DefaultMaxPayloadSize, result.options.maxPayloadSize)
}

func TestServerHandler_HandleRPC(t *testing.T) {
	// Set up a test exporter to capture span data
	exp := &testExporter{}
	trace.RegisterExporter(exp)
	defer trace.UnregisterExporter(exp)

	handler := NewServerHandler(func(options *gRPCOptions) {
		options.spanWithPayload = func(ctx context.Context, rpcStats stats.RPCStats) bool {
			return true
		}

		options.spanWithMetadata = func(ctx context.Context, rpcStats stats.RPCStats) bool {
			return true
		}
	})

	expectedStats := []stats.RPCStats{
		&stats.InHeader{
			Header: map[string][]string{
				"header1": {""},
			},
		},
		&stats.InTrailer{
			Trailer: map[string][]string{
				"trailer1": {""},
			},
		},
		&stats.OutHeader{
			Header: map[string][]string{
				"outHeader1": {""},
			},
		},
		&stats.OutTrailer{
			Trailer: map[string][]string{
				"outTrailer1": {""},
			},
		},
		&stats.InPayload{
			Payload: []byte(""),
		},
		&stats.OutPayload{
			Payload: []byte(""),
		},
		&stats.End{
			Error: fmt.Errorf(""),
		},
	}

	ctx, testSpan := trace.StartSpan(context.Background(), "test span", trace.WithSampler(trace.AlwaysSample()))

	for _, v := range expectedStats {
		handler.HandleRPC(ctx, v)
	}

	// End the span to trigger the exporter
	testSpan.End()

	// Verify the span was captured
	if len(exp.spans) == 0 {
		t.Fatal("Span was not captured by exporter")
	}

	capturedSpan := exp.spans[0]

	expectedMap := map[string]string{
		"request.header.header1":       "true",
		"request.trailer.trailer1":     "true",
		"response.header.outHeader1":   "true",
		"response.trailer.outTrailer1": "true",
	}

	// Extract attributes from the captured span
	resultMap := make(map[string]string, 4)
	for key := range capturedSpan.Attributes {
		// Check if the key is one of the expected keys
		keyStr := fmt.Sprint(key)
		if _, exists := expectedMap[keyStr]; exists {
			resultMap[keyStr] = "true"
		}
	}

	assert.Equal(t, expectedMap, resultMap)

	expectedAnnotations := []string{
		"Request payload", "Response payload", "Response error",
	}

	resultAnnotations := make([]string, 0, 3)
	for _, annotation := range capturedSpan.Annotations {
		resultAnnotations = append(resultAnnotations, annotation.Message)
	}

	assert.Equal(t, expectedAnnotations, resultAnnotations)
}

func TestMetadataToAttributes(t *testing.T) {
	expected := []trace.Attribute{trace.StringAttribute(fmt.Sprint("prefix.", expectedStr), "test value")}
	result := metadataToAttributes(metadata.MD{expectedStr: {"test value"}}, "prefix.", defaultMetadataMatcher)
	assert.Equal(t, expected, result)
}

func TestPayloadToAttributes(t *testing.T) {
	expected := trace.StringAttribute(expectedStr, "\"test value\"")
	result, ok, err := payloadToAttributes(expectedStr, "test value", 12)
	assert.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, expected, result[0])
}

func TestDefaultMetadataMatcher(t *testing.T) {
	resultStr, ok := defaultMetadataMatcher(expectedStr)
	assert.True(t, ok)
	assert.Equal(t, expectedStr, resultStr)
}

func TestAlwaysGRPC(t *testing.T) {
	assert.True(t, AlwaysGRPC(nil, nil))
}
