/*
 * Copyright 2018-Present the original author or authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package server

import (
	"github.com/projectriff/go-function-invoker/pkg/function"
	"reflect"
	"net/url"
	"plugin"
	"errors"
	"bytes"
)

const (
	// Headers

	ContentType   = "Content-Type"
	Accept        = "Accept"
	CorrelationId = "correlationId"
	Error         = "error"

	// Url query parameter that identifies the exported function to execute
	Handler = "handler"

	AssumedContentType = MediaType("text/plain")

	// Errors

	ContentTypeNotSupported = errorCode("error-client-content-type-unsupported")
	AcceptNotSupported      = errorCode("error-client-accept-type-unsupported")
	ErrorWhileUnmarshalling = errorCode("error-client-unmarshall")
	ErrorWhileMarshalling   = errorCode("error-client-marshall")
	InvocationError         = errorCode("error-server-function-invocation")
)

type pluginInvoker struct {
	fn            reflect.Value
	marshallers   []Marshaller
	unmarshallers []Unmarshaller
}

type errorCode string

type invokerError struct {
	code    errorCode
	cause   error
	message string
}

// requestReplyHeaders is the set of headers that should be copied from incoming message to outgoing message, if present
var requestReplyHeaders []string

func init() {
	requestReplyHeaders = []string{CorrelationId}
}

func (pi *pluginInvoker) invoke(in *function.Message) *function.Message {
	arg, err := pi.messageToFunctionArgs(in)
	if err != nil {
		return propagateHeaders(in, errorToMessage(err))
	}
	reflectiveArgs := [] reflect.Value{reflect.ValueOf(arg)}

	reflectiveResult := pi.fn.Call(reflectiveArgs)

	invocationResult := make([]interface{}, len(reflectiveResult))
	for i, v := range reflectiveResult {
		invocationResult[i] = v.Interface()
	}

	result, err := pi.functionResultToMessage(invocationResult, in)
	if err != nil {
		return propagateHeaders(in, errorToMessage(err))
	}
	return propagateHeaders(in, result)
}

// propagateHeaders modifies and returns out by adding to it headers from in that belong to the requestReplyHeaders list
func propagateHeaders(in *function.Message, out *function.Message) *function.Message {
	for _, h := range requestReplyHeaders {
		out.Headers[h] = in.Headers[h]
	}
	return out
}

func (pi *pluginInvoker) messageToFunctionArgs(in *function.Message) (interface{}, error) {
	contentType := AssumedContentType
	if ct, ok := in.Headers[ContentType]; ok {
		contentType = MediaType(ct.Values[0])
	}
	argType := pi.fn.Type().In(0)
	for _, um := range pi.unmarshallers {
		if um.canUnmarshall(argType, contentType) {
			result, err := um.unmarshall(bytes.NewReader(in.Payload), argType, contentType)
			if err != nil {
				return nil, invokerError{code: ErrorWhileUnmarshalling, cause: err}
			} else {
				return result, nil
			}
		}
	}
	return nil, unsupportedContentType(contentType)
}

func (invoker *pluginInvoker) functionResultToMessage(values []interface{}, in *function.Message) (*function.Message, error) {

	var payload []byte
	var contentType MediaType

	if len(values) == 1 || values[1] == nil {
		// successful invocation
		supportedMarshallers := make(map[MediaType]Marshaller)
		for _, m := range invoker.marshallers {
			t := reflect.TypeOf(values[0])
			offers := m.supportedMediaTypes(t)
			for _, o := range offers {
				if _, present := supportedMarshallers[o]; !present {
					supportedMarshallers[o] = m
				}
			}
		}
		chosen, contentType := bestMarshaller(in, supportedMarshallers)
		if chosen != nil {
			var buffer bytes.Buffer
			err := chosen.marshall(values[0], &buffer, contentType)
			if err != nil {
				return nil, invokerError{code: ErrorWhileMarshalling, cause: err}
			}
			payload = buffer.Bytes()
		} else {
			return nil, invokerError{code: AcceptNotSupported}
		}

	} else {
		// invocation error
		err := values[1].(error)
		return nil, invokerError{code: InvocationError, cause: err}
	}

	return &function.Message{Payload: payload,
		Headers: map[string]*function.Message_HeaderValue{ContentType: &function.Message_HeaderValue{Values: []string{string(contentType)}}}}, nil

}

func errorToMessage(err error) *function.Message {
	headers := make(map[string]*function.Message_HeaderValue)
	payload := []byte(err.Error())
	headers[ContentType] = &function.Message_HeaderValue{Values: []string{"text/plain"}}
	if ie, ok := err.(invokerError); ok {
		headers[Error] = &function.Message_HeaderValue{Values: []string{string(ie.code)}}
	}
	return &function.Message{Payload: payload, Headers: headers}
}

func newInvoker(fnUri string) (*pluginInvoker, error) {
	result := pluginInvoker{}

	url, err := url.Parse(fnUri)
	if err != nil {
		return &result, err
	}
	if url.Scheme != "" && url.Scheme != "file" {
		return &result, errors.New("Unsupported scheme in function URI: " + fnUri)
	}
	lib, err := plugin.Open(url.Path)
	if err != nil {
		return &result, err
	}
	fnName := url.Query()[Handler][0]
	fnSymbol, err := lib.Lookup(fnName)
	if err != nil {
		return &result, err
	}
	result.fn = reflect.ValueOf(fnSymbol)
	if result.fn.Type().NumIn() != 1 {
		return &result, errors.New("Provided function should accept exactly one parameter: " + result.fn.Type().String())
	}
	nbOut := result.fn.Type().NumOut()
	if nbOut > 2 {
		return &result, errors.New("Provided function should return at most 2 results: " + result.fn.Type().String())
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()
	if nbOut > 1 && !result.fn.Type().Out(nbOut - 1).AssignableTo(errorType) {
		return &result, errors.New("The last result of provided function should be of 'error' type: " + result.fn.Type().String())
	}

	result.marshallers = []Marshaller{&jsonMarshalling{}, &textMarshalling{}}
	result.unmarshallers = []Unmarshaller{&jsonMarshalling{}, &textMarshalling{}}
	return &result, err

}

func unsupportedContentType(ct MediaType) invokerError {
	return invokerError{
		code:    ContentTypeNotSupported,
		message: "Unsupported Content-Type: " + string(ct),
	}
}

func (ie invokerError) Error() string {
	if ie.cause != nil {
		return ie.cause.Error()
	} else {
		return ie.message
	}
}
