package server

import (
	"runtime"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	"os/exec"
	"os"
	"github.com/projectriff/go-function-invoker/pkg/function"
	"fmt"
	"github.com/onsi/gomega/types"
)

const (
	builtPlugin = "../../fixtures/function_plugin.so"
)

var _ = BeforeSuite(func() {
	// Go plugins are only supported on *nix and Mac
	Expect(runtime.GOOS).NotTo(Equal("windows"))

	command := exec.Command("go", "build", "-buildmode=plugin", "-o", builtPlugin, sourceOf(builtPlugin))
	command.Stderr = os.Stderr
	command.Stdout = os.Stdout
	err := command.Run()
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	os.Remove(builtPlugin)
})

type TestCase struct {
	ContentType   string
	Accept        string
	In            []byte
	Expected      types.GomegaMatcher
	ExpectedError errorCode
}

var _ = Describe("PluginInvoker", func() {

	var (
		invoker *pluginInvoker
		handler string
	)

	runTest := func(tc TestCase) {
		headers := make(map[string]string)
		if tc.Accept != "" {
			headers[Accept] = tc.Accept
		}
		if tc.ContentType != "" {
			headers[ContentType] = tc.ContentType
		}
		m := &function.Message{Payload: tc.In, Headers: convert(headers)}
		r := invoker.invoke(m)
		if tc.Expected != nil {
			Expect(r.Payload).To(tc.Expected)
		}
		if tc.ExpectedError != "" {
			Expect(r.Headers[Error].GetValues()[0], Equal(tc.ExpectedError))
		}

	}

	JustBeforeEach(func() {
		var err error

		invoker, err = newInvoker(fmt.Sprintf("%s?%s=%s", builtPlugin, Handler, handler))
		Expect(err).NotTo(HaveOccurred())
	})

	Context("with functions that accept a string", func() {
		BeforeEach(func() {
			handler = "StringInStringOut"
		})

		DescribeTable("using input Content-Type", runTest,
			Entry("should support text/plain in", TestCase{
				ContentType: "text/plain",
				In:          []byte("world"),
				Expected:    Equal([]byte("Hello world")),
			}),
			Entry("should support application/json in", TestCase{
				ContentType: "application/json",
				In:          []byte(`"world"`),
				Expected:    Equal([]byte("Hello world")),
			}),
		)
	})

	Context("with functions that emit a string", func() {
		BeforeEach(func() {
			handler = "StringInStringOut"
		})

		DescribeTable("using output Accept", runTest,
			Entry("should support text/plain out", TestCase{
				ContentType: "text/plain",
				Accept:      "text/plain",
				In:          []byte("world"),
				Expected:    Equal([]byte("Hello world")),
			}),
			Entry("should support application/json out", TestCase{
				ContentType: "text/plain",
				Accept:      "application/json",
				In:          []byte("world"),
				Expected:    Equal([]byte("\"Hello world\"\n")),
			}),
		)
	})

	Context("whatever function that can be invoked", func() {
		BeforeEach(func() {
			handler = "StringInChannelOut"
		})

		DescribeTable("possible errors are", runTest,
			Entry("unsupported Content-Type", TestCase{
				ContentType:   "bogus/focus",
				In:            []byte("world"),
				ExpectedError: ContentTypeNotSupported,
				Expected:      ContainSubstring("bogus/focus"),
			}),
			Entry("unsupported Accept", TestCase{
				ContentType:   "text/plain",
				Accept:        "idont/havethat",
				In:            []byte("world"),
				ExpectedError: AcceptNotSupported,
			}),
			Entry("error while unmarshalling input", TestCase{
				ContentType:   "application/json",
				In:            []byte("foo"), // foo without quotes is not a json string. It's nothing parseable
				ExpectedError: ErrorWhileUnmarshalling,
			}),
			Entry("error while marshalling result", TestCase{
				ContentType:   "text/plain",
				Accept:        "application/json",
				In:            []byte("world"),
				ExpectedError: ErrorWhileMarshalling,
			}),
			Entry("function invocation error (using go's error return type)", TestCase{
				ContentType:   "text/plain",
				In:            []byte("fail"),
				Expected:      Equal([]byte("Oops")),
				ExpectedError: InvocationError,
			}),
		)
	})

	Context("with any function that can be invoked", func() {
		BeforeEach(func() {
			handler = "StringInStringOut"
		})

		It("should receive back correlationId if provided", func() {
			headers := map[string]string{CorrelationId: "foobar"}
			m := &function.Message{Payload: []byte(""), Headers: convert(headers)}
			r := invoker.invoke(m)
			Expect(r.Headers[CorrelationId].GetValues()[0], Equal("foobar"))
		})
	})

})

func sourceOf(lib string) string {
	result := []rune(lib)
	result[len(lib)-len("so")] = 'g'
	return string(result)
}

func convert(hs map[string]string) map[string]*function.Message_HeaderValue {
	result := make(map[string]*function.Message_HeaderValue, len(hs))
	for k, v := range hs {
		result[k] = &function.Message_HeaderValue{Values: []string{v}}
	}
	return result
}
