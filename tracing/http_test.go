package tracing

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/plugin/ochttp"
	"go.opencensus.io/trace"
)

var testHTTPOpts = &httpOptions{}
var expectedStr = "test"

func Test_truncatePayload(t *testing.T) {
	tests := []struct {
		in      []byte
		out     []byte
		outFlag bool

		limit int
	}{
		{
			in:      []byte("Hello World"),
			out:     []byte("Hello World"),
			outFlag: false,
			limit:   10000000,
		},
		{
			in:      []byte("Hello World"),
			out:     []byte("He..."),
			outFlag: true,
			limit:   5,
		},
		{
			in:      []byte("Hello"),
			out:     []byte("Hello"),
			outFlag: false,
			limit:   5,
		},
	}

	for _, tt := range tests {
		out, reduced := truncatePayload(tt.in, tt.limit)
		if tt.outFlag != reduced {
			t.Errorf("Unexpected result expected %t, got %t", tt.outFlag, reduced)
		}

		if string(out) != string(tt.out) {
			t.Errorf("Unexpected result\n\texpected %q\n\tgot %q", tt.out, out)
		}
	}
}

func Test_obfuscate(t *testing.T) {
	tests := []struct {
		in  string
		out string
	}{
		{
			in:  "HelloWorld",
			out: "He...",
		},
		{
			in:  "",
			out: "...",
		},
		{
			in:  "H",
			out: "...",
		},
	}

	for _, tt := range tests {
		out := obfuscate(tt.in)
		if out != tt.out {
			t.Errorf("Unexpected result\n\texpected %q\n\tgot %q", tt.out, out)
		}
	}
}

func TestDefaultHTTPOptions(t *testing.T) {
	expected := &httpOptions{
		headerMatcher:  defaultHeaderMatcher,
		maxPayloadSize: 1048576,
	}

	result := defaultHTTPOptions()
	expectedHeader, expectedBool := expected.headerMatcher(expectedStr)
	resultHeader, resultBool := result.headerMatcher(expectedStr)
	assert.True(t, expectedBool)
	assert.Equal(t, expectedBool, resultBool)
	assert.Equal(t, expectedHeader, resultHeader)
	assert.Equal(t, expected.maxPayloadSize, result.maxPayloadSize)
}

func TestWithHeadersAnnotation(t *testing.T) {
	option := WithHeadersAnnotation(func(r *http.Request) bool {
		return true
	})
	option(testHTTPOpts)
	assert.True(t, testHTTPOpts.spanWithHeaders(nil))
}

func TestWithHeaderMatcher(t *testing.T) {
	option := WithHeaderMatcher(defaultHeaderMatcher)
	option(testHTTPOpts)
	resultStr, ok := testHTTPOpts.headerMatcher(expectedStr)
	assert.True(t, ok)
	assert.Equal(t, expectedStr, resultStr)
}

func TestWithPayloadAnnotation(t *testing.T) {
	option := WithPayloadAnnotation(func(r *http.Request) bool {
		return true
	})
	option(testHTTPOpts)
	assert.True(t, testHTTPOpts.spanWithPayload(nil))
}

func TestWithHTTPPayloadSize(t *testing.T) {
	option := WithHTTPPayloadSize(333)
	option(testHTTPOpts)
	assert.Equal(t, 333, testHTTPOpts.maxPayloadSize)
}

func TestNewMiddleware(t *testing.T) {
	handlerFunc := NewMiddleware(func(options *httpOptions) {
		options.spanWithHeaders = func(r *http.Request) bool {
			return true
		}
	})

	handler := handlerFunc(&httpHandlerMock{})
	ocHandler, ok := handler.(*ochttp.Handler)
	assert.True(t, ok)

	result, ok := (ocHandler.Handler).(*Handler)
	assert.True(t, ok)
	assert.True(t, result.options.spanWithHeaders(nil))
}

func TestHandler_ServeHTTP(t *testing.T) {
	// Set up a test exporter to capture span data
	exp := &testExporter{}
	trace.RegisterExporter(exp)
	defer trace.UnregisterExporter(exp)

	handlerFunc := NewMiddleware(func(options *httpOptions) {
		options.spanWithHeaders = func(r *http.Request) bool {
			return true
		}

		options.spanWithPayload = func(r *http.Request) bool {
			return true
		}
	})

	ctx, testSpan := trace.StartSpan(context.Background(), "test span", trace.WithSampler(trace.AlwaysSample()))

	r, _ := http.NewRequest("", "", bytes.NewBuffer([]byte("test body")))
	r.Header = map[string][]string{
		"test1": {"test11"},
		"test2": {"test22"},
	}
	r = r.WithContext(ctx)

	w := httptest.NewRecorder()
	w.Header().Add("test3", "")

	result := &httpHandlerMock{}
	handler := handlerFunc(result)
	handler.ServeHTTP(w, r)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// End the span to trigger the exporter
	testSpan.End()

	// Verify the span was captured
	if len(exp.spans) == 0 {
		t.Fatal("Span was not captured by exporter")
	}

	capturedSpan := exp.spans[0]

	// Test that Span attributes were populated with Headers
	assert.Len(t, capturedSpan.Attributes, 8)

	resultHeadersMap := make(map[string]string)
	for key, val := range capturedSpan.Attributes {
		resultHeadersMap[fmt.Sprint(key)] = fmt.Sprint(val)
	}
	assert.Equal(t, "test11", resultHeadersMap[fmt.Sprint(RequestHeaderAnnotationPrefix, "test1")])
	assert.Equal(t, "test22", resultHeadersMap[fmt.Sprint(RequestHeaderAnnotationPrefix, "test2")])
	assert.Equal(t, "", resultHeadersMap[fmt.Sprint(ResponseHeaderAnnotationPrefix, "Test3")])

	// Test that Span annotations were populated with payload attributes and annotation messages
	assert.Len(t, capturedSpan.Annotations, 2)
	resultRequestPayloadMsg := capturedSpan.Annotations[0].Message
	resultResponsePayloadMsg := capturedSpan.Annotations[1].Message
	assert.Equal(t, "Request payload", resultRequestPayloadMsg)
	assert.Equal(t, "Response payload", resultResponsePayloadMsg)

	// Test that Span contains given payload
	requestPayloadAttr := capturedSpan.Annotations[0].Attributes
	assert.Len(t, requestPayloadAttr, 1)
	var resultPayload string
	for _, val := range requestPayloadAttr {
		resultPayload = fmt.Sprint(val)
		break
	}
	assert.Equal(t, "test body", resultPayload)
}

func TestHeadersToAttributes(t *testing.T) {
	expected := append(make([]trace.Attribute, 0, 2), trace.StringAttribute("prefix-test1", "test11"), trace.StringAttribute("prefix-test2", "test22"))
	testHeaders := map[string][]string{
		"test1": {"test11"},
		"test2": {"test22"},
	}

	result := headersToAttributes(testHeaders, "prefix-", defaultHeaderMatcher)

	assert.Len(t, result, 2)
	for _, attribute := range result {
		found := false
		for _, expAttribute := range expected {
			if reflect.DeepEqual(expAttribute, attribute) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Attribute %+v not found in result", attribute)
		}
	}
}

func TestMarkSpanTruncated(t *testing.T) {
	// Set up a test exporter to capture span data
	exp := &testExporter{}
	trace.RegisterExporter(exp)
	defer trace.UnregisterExporter(exp)

	_, span := trace.StartSpan(context.Background(), "test span", trace.WithSampler(trace.AlwaysSample()))
	markSpanTruncated(span)
	span.End()

	// Verify the span was captured
	if len(exp.spans) == 0 {
		t.Fatal("Span was not captured by exporter")
	}

	capturedSpan := exp.spans[0]
	assert.Len(t, capturedSpan.Attributes, 1)

	var resultKey, resultValue string
	for key, val := range capturedSpan.Attributes {
		resultKey = fmt.Sprint(key)
		resultValue = fmt.Sprint(val)
	}
	assert.Equal(t, TruncatedMarkerKey, resultKey)
	assert.Equal(t, TruncatedMarkerValue, resultValue)
}

func TestNewResponseWrapper(t *testing.T) {
	expected := &bytes.Buffer{}
	result := newResponseWrapper(&httpResponseWriterMock{})
	assert.NotNil(t, result.ResponseWriter)
	assert.Equal(t, expected, result.buffer)
}

func TestResponseBodyWrapper_Write(t *testing.T) {
	wrapper := newResponseWrapper(&httpResponseWriterMock{})
	result, err := wrapper.Write([]byte("3"))
	assert.NoError(t, err)
	assert.Equal(t, 0, result)
}

func TestDefaultHeaderMatcher(t *testing.T) {
	result, ok := defaultHeaderMatcher(expectedStr)
	assert.Equal(t, expectedStr, result)
	assert.True(t, ok)
}

func TestAlwaysHTTP(t *testing.T) {
	test, _ := http.NewRequest("", "", nil)
	result := AlwaysHTTP(test)
	assert.True(t, result)
}

type httpResponseWriterMock struct {
	http.ResponseWriter
}

func (fake *httpResponseWriterMock) Write(_ []byte) (int, error) {
	return 0, nil
}

type httpHandlerMock struct {
	http.Handler
	writer  http.ResponseWriter
	request *http.Request
}

func (fake *httpHandlerMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fake.writer = w
	fake.request = r
}
