package sqstransport

import (
	"context"

	"github.com/go-kit/kit/endpoint"
	"github.com/go-kit/kit/transport"
)

type Option func(*Subscriber)

// UseDecodeRequest sets the decoder function which is required.
func UseDecodeRequest(decodeRequest DecodeRequestFunc) Option {
	return func(s *Subscriber) {
		s.decodeRequest = decodeRequest
	}
}

// UseHandler sets the handler function which is required.
func UseHandler(handler endpoint.Endpoint) Option {
	return func(s *Subscriber) {
		s.handler = handler
	}
}

// UseInputFactory sets the InputFactory function which is required.
// It must return a non-nil params.
// It can return nil for optFns.
func UseInputFactory(inputFactory InputFactory) Option {
	return func(s *Subscriber) {
		s.inputFactory = inputFactory
	}
}

// UseResponseHandler adds a ResponseHandler function which is required. Can be called multiple times.
// Any actions required after executing handler can take place here.
// Like deleting the message after being successfully processed.
func UseResponseHandler(responseHandler ...ResponseFunc) Option {
	return func(s *Subscriber) {
		s.responseHandler = append(s.responseHandler, responseHandler...)
	}
}

// WithBefore adds RequestFunc which is optional. Can be called multiple times.
// Can be used for starting a keep-in-flight hearbeat - an example.
// They run before DecodeRequest and can put additional data inside the context.
// If returns a nil context, it causes a panic.
func WithBefore(before ...RequestFunc) Option {
	return func(s *Subscriber) {
		s.before = append(s.before, before...)
	}
}

// WithAfterBatch adds an AfterBatch function which is optional.
// It is called after a batch of messages passed to the Runner.
func WithAfterBatch(afterBatch AfterBatchFunc) Option {
	return func(s *Subscriber) {
		s.afterBatch = afterBatch
	}
}

// WithBaseContext sets the base context for the subscriber.
// If not provided, will be context.Background.
func WithBaseContext(ctxFac func() context.Context) Option {
	return func(s *Subscriber) {
		s.baseContext = ctxFac
	}
}

// WithRunner sets the Runner.
// If not provided, the default runner will be used.
// All the Befor functions, decoding the message, handling the message
// and handling the response are executed by the Runner.
func WithRunner(runner Runner) Option {
	return func(s *Subscriber) {
		s.runner = runner
	}
}

// WithErrorHandler sets the error handler which is optional.
func WithErrorHandler(errorHandler transport.ErrorHandler) Option {
	return func(s *Subscriber) {
		s.errorHandler = errorHandler
	}
}
