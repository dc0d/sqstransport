//go:generate moq -out client_spy_test.go . Client:ClientSpy
//go:generate moq -out transport_error_handler_spy_test.go . transportErrorHandler:TransportErrorHandlerSpy

package sqstransport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Subscriber_should_stop_when_shutdown_is_called(t *testing.T) {
	t.Parallel()

	stopped := make(chan struct{})
	sut := New(
		UseHandler(nopHandler),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
		WithBaseContext(context.TODO),
		WithRunner(newDefaultRunner()),
	)
	sut.onExit = func() { close(stopped) }

	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		_ = sut.Serve(context.Background(), mockClient10msec())
	}()
	<-serverStarted

	sut.Shutdown()

	assert.Eventually(t, func() bool {
		select {
		case <-stopped:
			return true
		default:
		}

		return false
	}, time.Millisecond*100, time.Millisecond*20)
}

func Test_Subscriber_should_stop_when_initial_parent_context_is_canceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan struct{})
	sut := New(
		UseHandler(nopHandler),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
	)
	sut.onExit = func() { close(stopped) }

	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		_ = sut.Serve(ctx, mockClient10msec())
	}()
	<-serverStarted

	cancel()

	assert.Eventually(t, func() bool {
		select {
		case <-stopped:
			return true
		default:
		}

		return false
	}, time.Millisecond*100, time.Millisecond*20)
}

func Test_Subscriber_should_error_if_Serve_called_more_than_once(t *testing.T) {
	t.Parallel()

	client := mockClient10msec()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sut := New(
		UseHandler(nopHandler),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
	)

	expectedError := ErrAlreadyStarted

	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		_ = sut.Serve(ctx, client)
	}()
	<-serverStarted

	actualError := sut.Serve(ctx, client)
	assert.Equal(t, expectedError, actualError)
}

func Test_Subscriber_should_call_the_handler_on_first_new_message(t *testing.T) {
	t.Parallel()

	client := mockClient10msec()
	expectedInput := makeReceiveMessageInput()
	inputFactory := makeInputFactory(expectedInput)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	actualRequestLock := sync.Mutex{}
	var actualRequest interface{}

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			actualRequestLock.Lock()
			defer actualRequestLock.Unlock()
			actualRequest = request
			return nil, nil
		}),
		UseInputFactory(inputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
	)

	go func() { _ = sut.Serve(ctx, client) }()

	assert.Eventually(t, func() bool {
		if len(client.ReceiveMessageCalls()) == 0 {
			return false
		}

		return client.ReceiveMessageCalls()[0].Params == expectedInput
	}, time.Millisecond*100, time.Millisecond*20)

	assert.Eventually(t, func() bool {
		actualRequestLock.Lock()
		defer actualRequestLock.Unlock()

		return strings.HasPrefix(fmt.Sprint(actualRequest), stringMessagePrefix)
	}, time.Millisecond*100, time.Millisecond*20)
}

func Test_Subscriber_should_call_the_handler_on_each_new_message(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	incomingLock := sync.Mutex{}
	var incoming []string

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			incomingLock.Lock()
			defer incomingLock.Unlock()
			incoming = append(incoming, fmt.Sprint(request))
			return nil, nil
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
	)

	go func() { _ = sut.Serve(ctx, mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		incomingLock.Lock()
		defer incomingLock.Unlock()

		if len(incoming) < 10 {
			return false
		}

		for _, msg := range incoming {
			if !strings.HasPrefix(msg, stringMessagePrefix) {
				return false
			}
		}

		return true
	}, time.Millisecond*300, time.Millisecond*20)
}

func Test_Subscriber_should_pass_the_base_context_to_handler(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	baseCtx, baseCancel := context.WithCancel(context.Background())
	// base context is already canceled
	baseCancel()

	receivedCtxCanceledErr := false
	lockReceivedCtxCanceledErr := &sync.Mutex{}

	errorHandler := &TransportErrorHandlerSpy{
		HandleFunc: func(ctx context.Context, err error) {
			lockReceivedCtxCanceledErr.Lock()
			defer lockReceivedCtxCanceledErr.Unlock()
			receivedCtxCanceledErr = true
		},
	}

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			panic("should not be called")
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
		WithBaseContext(func() context.Context { return baseCtx }),
		WithErrorHandler(errorHandler),
	)

	go func() { _ = sut.Serve(ctx, mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		lockReceivedCtxCanceledErr.Lock()
		defer lockReceivedCtxCanceledErr.Unlock()

		return receivedCtxCanceledErr
	}, time.Millisecond*300, time.Millisecond*20)
}

func Test_Subscriber_should_call_the_ResponseHandler_after_handler(t *testing.T) {
	t.Parallel()

	type contextKey string
	const valKey contextKey = "a context value key"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const expectedResponse = "a response"

	actualResponseLock := &sync.Mutex{}
	var actualResponse interface{}

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return expectedResponse, nil
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(func(ctx context.Context, msg types.Message, response interface{}) context.Context {
			actualResponseLock.Lock()
			defer actualResponseLock.Unlock()
			actualResponse = response
			return context.WithValue(ctx, valKey, 100)
		}),
		UseResponseHandler(func(ctx context.Context, msg types.Message, response interface{}) context.Context {
			require.Equal(t, 100, ctx.Value(valKey))
			return ctx
		}),
	)

	go func() { _ = sut.Serve(ctx, mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		actualResponseLock.Lock()
		defer actualResponseLock.Unlock()

		return expectedResponse == actualResponse
	}, time.Millisecond*100, time.Millisecond*20)
}

func Test_Subscriber_should_call_the_error_handler_on_returned_error_from_receive_message(t *testing.T) {
	t.Parallel()

	expectedError := errors.New("an error")

	client := &ClientSpy{
		ReceiveMessageFunc: func(
			ctx context.Context,
			params *sqs.ReceiveMessageInput,
			optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
			return nil, expectedError
		},
	}

	errorHandler := &TransportErrorHandlerSpy{
		HandleFunc: func(ctx context.Context, err error) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sut := New(
		UseHandler(unreachableHandler),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
		WithErrorHandler(errorHandler),
	)

	go func() { _ = sut.Serve(ctx, client) }()

	assert.Eventually(t, func() bool {
		if len(errorHandler.HandleCalls()) == 0 {
			return false
		}

		return errorHandler.HandleCalls()[0].Err == expectedError
	}, time.Millisecond*300, time.Millisecond*20)
}

func Test_Subscriber_should_continue_if_error_handler_is_not_provided(t *testing.T) {
	t.Parallel()

	expectedError := errors.New("an error")

	client := &ClientSpy{
		ReceiveMessageFunc: func(
			ctx context.Context,
			params *sqs.ReceiveMessageInput,
			optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
			return nil, expectedError
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sut := New(
		UseHandler(unreachableHandler),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
	)

	go func() { _ = sut.Serve(ctx, client) }()

	assert.Eventually(t, func() bool {
		return len(client.ReceiveMessageCalls()) > 3
	}, time.Millisecond*300, time.Millisecond*20)
}

func Test_Subscriber_should_call_the_error_handler_on_returned_error_from_decode_request(t *testing.T) {
	t.Parallel()

	expectedDecoderError := errors.New("an error")
	expectedError := &DecoderError{
		Err: expectedDecoderError,
		Msg: types.Message{Body: aws.String(msg1)},
	}

	decodeRequest := func(ctx context.Context, msg types.Message) (request interface{}, err error) {
		return nil, expectedDecoderError
	}

	errorHandler := &TransportErrorHandlerSpy{
		HandleFunc: func(ctx context.Context, err error) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return nil, nil
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(decodeRequest),
		UseResponseHandler(nopResponseHandler...),
		WithErrorHandler(errorHandler),
	)

	go func() { _ = sut.Serve(ctx, mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		if len(errorHandler.HandleCalls()) == 0 {
			return false
		}

		actual, ok := errorHandler.HandleCalls()[0].Err.(*DecoderError)
		if !ok {
			return false
		}
		if !assert.Equal(t, expectedError.Err, actual.Err) {
			return false
		}
		return assert.Equal(t, expectedError.Msg, actual.Msg)
	}, time.Millisecond*300, time.Millisecond*20)
}

//nolint:dupl
func Test_Subscriber_should_call_the_error_handler_on_returned_error_from_handler_endpoint(t *testing.T) {
	t.Parallel()

	data := msg1
	errorFromHandler := errors.New("an error")
	expectedError := &HandlerError{
		Err:     errorFromHandler,
		Request: data,
		Msg:     types.Message{Body: aws.String(data)},
	}

	errorHandler := &TransportErrorHandlerSpy{
		HandleFunc: func(ctx context.Context, err error) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return nil, errorFromHandler
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
		WithErrorHandler(errorHandler),
	)

	go func() { _ = sut.Serve(ctx, mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		if len(errorHandler.HandleCalls()) == 0 {
			return false
		}
		actual, ok := errorHandler.HandleCalls()[0].Err.(*HandlerError)
		if !ok {
			return false
		}
		if !assert.Equal(t, expectedError.Err, actual.Err) {
			return false
		}
		if !assert.Equal(t, expectedError.Request, actual.Request) {
			return false
		}
		return assert.Equal(t, expectedError.Msg, actual.Msg)
	}, time.Millisecond*300, time.Millisecond*20)
}

//nolint:dupl
func Test_Subscriber_should_not_call_the_response_handler_on_returned_error_from_handler_endpoint(t *testing.T) {
	t.Parallel()

	data := msg1
	errorFromHandler := errors.New("an error")
	expectedError := &HandlerError{
		Err:     errorFromHandler,
		Request: data,
		Msg:     types.Message{Body: aws.String(data)},
	}

	errorHandler := &TransportErrorHandlerSpy{
		HandleFunc: func(ctx context.Context, err error) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return nil, errorFromHandler
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(panickingResponseHandler...),
		WithErrorHandler(errorHandler),
	)

	go func() { _ = sut.Serve(ctx, mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		if len(errorHandler.HandleCalls()) == 0 {
			return false
		}
		actual, ok := errorHandler.HandleCalls()[0].Err.(*HandlerError)
		if !ok {
			return false
		}
		if !assert.Equal(t, expectedError.Err, actual.Err) {
			return false
		}
		if !assert.Equal(t, expectedError.Request, actual.Request) {
			return false
		}
		return assert.Equal(t, expectedError.Msg, actual.Msg)
	}, time.Millisecond*300, time.Millisecond*20)
}

func Test_Subscriber_should_call_the_before_functions(t *testing.T) {
	t.Parallel()

	type contextKey string
	const counterKey contextKey = "a_counter"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		handlerCtxLock = &sync.Mutex{}
		handlerCtx     context.Context
	)

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			handlerCtxLock.Lock()
			defer handlerCtxLock.Unlock()
			handlerCtx = ctx

			return nil, nil
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
		WithBefore(func(ctx context.Context, msg types.Message) context.Context {
			return context.WithValue(ctx, counterKey, 1)
		}),
		WithBefore(func(ctx context.Context, msg types.Message) context.Context {
			previous := ctx.Value(counterKey).(int)
			return context.WithValue(ctx, counterKey, previous+1)
		}),
	)

	go func() { _ = sut.Serve(ctx, mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		handlerCtxLock.Lock()
		defer handlerCtxLock.Unlock()
		if handlerCtx == nil {
			return false
		}

		v, ok := handlerCtx.Value(counterKey).(int)
		if !ok {
			return false
		}

		return v >= 2
	}, time.Millisecond*100, time.Millisecond*20)
}

func Test_Subscriber_should_call_AfterBatch_after_calling_the_handler_for_received_messages(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var afterBatchCalls int64

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return nil, nil
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
		WithAfterBatch(func(ctx context.Context) {
			atomic.AddInt64(&afterBatchCalls, 1)
		}),
	)

	go func() { _ = sut.Serve(ctx, mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&afterBatchCalls) > 3
	}, time.Millisecond*100, time.Millisecond*20)
}

func Test_Subscriber_should_panic_if_any_before_function_returns_a_nil_context(t *testing.T) {
	t.Parallel()

	sut := &Subscriber{
		before: []RequestFunc{
			func(ctx context.Context, msg types.Message) context.Context {
				return nil
			},
		},
	}

	assert.Panics(t, func() { sut.runBefore(context.Background(), types.Message{}) })
}

func Test_Subscriber_should_panic_if_any_response_handler_function_returns_a_nil_context(t *testing.T) {
	t.Parallel()

	sut := &Subscriber{
		responseHandler: []ResponseFunc{
			func(ctx context.Context, msg types.Message, response interface{}) context.Context {
				return nil
			},
		},
	}

	assert.Panics(t, func() { sut.runResponseHandler(context.Background(), types.Message{}, nil) })
}

func Test_Subscriber_runHandler_should_create_a_request_scoped_context(t *testing.T) {
	t.Parallel()

	var (
		scopedCtx     context.Context
		scopedCtxLock = &sync.Mutex{}
	)

	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return "OK", nil
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(func(c context.Context, m types.Message) (request interface{}, err error) { return m, nil }),
		UseResponseHandler(func(ctx context.Context, msg types.Message, response interface{}) context.Context {
			scopedCtxLock.Lock()
			defer scopedCtxLock.Unlock()
			scopedCtx = ctx
			return ctx
		}),
	)

	_ = sut.init()

	msg := types.Message{
		Body: aws.String("a message"),
	}

	sut.runHandler(context.Background(), msg)

	assert.Eventually(t, func() bool {
		scopedCtxLock.Lock()
		defer scopedCtxLock.Unlock()

		return scopedCtx.Err() == context.Canceled
	}, time.Millisecond*300, time.Millisecond*20)
}

func Test_Subscriber_New(t *testing.T) {
	t.Parallel()

	t.Run(`should panic if Handler is nil`, func(t *testing.T) {
		assert.PanicsWithValue(t, "Handler is required", func() {
			_ = New()
		})
	})

	t.Run(`should panic if InputFactory is nil`, func(t *testing.T) {
		assert.PanicsWithValue(t, "InputFactory is required", func() {
			_ = New(
				UseHandler(nopHandler),
			)
		})
	})

	t.Run(`should panic if DecodeRequest is nil`, func(t *testing.T) {
		assert.PanicsWithValue(t, "DecodeRequest is required", func() {
			_ = New(
				UseHandler(nopHandler),
				UseInputFactory(defaultInputFactory),
			)
		})
	})

	t.Run(`should panic if ResponseHandler is nil`, func(t *testing.T) {
		assert.PanicsWithValue(t, "ResponseHandler is required", func() {
			_ = New(
				UseHandler(nopHandler),
				UseInputFactory(defaultInputFactory),
				UseDecodeRequest(nopDecodeRequest),
			)
		})
	})
}

func Test_Subscriber_init(t *testing.T) {
	t.Parallel()

	t.Run(`should error if called more than once`, func(t *testing.T) {
		sut := New(
			UseHandler(unreachableHandler),
			UseInputFactory(func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) { panic("n/a") }),
			UseDecodeRequest(func(context.Context, types.Message) (request interface{}, err error) { panic("n/a") }),
			UseResponseHandler(panickingResponseHandler...),
		)

		expectedError := ErrAlreadyStarted

		actualError := sut.init()
		assert.NoError(t, actualError)

		actualError = sut.init()
		assert.Equal(t, expectedError, actualError)
	})
}

func Test_HandlerError_Error(t *testing.T) {
	expectedErrorString := expectedErrorString1

	sut := &HandlerError{Err: errors.New(expectedErrorString)}

	assert.Equal(t, expectedErrorString, sut.Error())
}

func Test_HandlerError_Error_with_nil_Err(t *testing.T) {
	expectedErrorString := defaultHandlerErrorMsh

	sut := &HandlerError{}

	assert.Equal(t, expectedErrorString, sut.Error())
}

func Test_DecoderError_Error(t *testing.T) {
	expectedErrorString := expectedErrorString1

	sut := &DecoderError{Err: errors.New(expectedErrorString)}

	assert.Equal(t, expectedErrorString, sut.Error())
}

func Test_DecoderError_Error_with_nil_Err(t *testing.T) {
	expectedErrorString := defaultDecoderErrorMsh

	sut := &DecoderError{}

	assert.Equal(t, expectedErrorString, sut.Error())
}

func ExampleSubscriber() {
	client := mockClient10msec()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handled := make(chan struct{})
	sut := New(
		UseHandler(func(ctx context.Context, request interface{}) (response interface{}, err error) {
			select {
			case <-handled:
				return nil, nil
			default:
			}

			defer close(handled)

			// processing the request inside the endpoint
			fmt.Println(request)

			return nil, nil
		}),
		UseInputFactory(defaultInputFactory),
		UseDecodeRequest(nopDecodeRequest),
		UseResponseHandler(nopResponseHandler...),
	)

	go func() { _ = sut.Serve(ctx, client) }()

	<-handled

	// Output:
	// MSG-1
}

func mockClient10msec() *ClientSpy {
	msgSeq := 0
	client := &ClientSpy{
		ReceiveMessageFunc: func(
			ctx context.Context,
			params *sqs.ReceiveMessageInput,
			optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			if msgSeq > 0 {
				time.Sleep(time.Millisecond * 10)
			}

			msgSeq++

			msg := types.Message{
				Body: aws.String(fmt.Sprintf(stringMessagePrefix+"%v", msgSeq)),
			}

			result := &sqs.ReceiveMessageOutput{
				Messages: []types.Message{msg},
			}

			return result, nil
		},
	}

	return client
}

func defaultInputFactory() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) {
	return makeReceiveMessageInput(), nil
}

func makeInputFactory(expectedInput *sqs.ReceiveMessageInput) func() (*sqs.ReceiveMessageInput, []func(*sqs.Options)) {
	return func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) {
		return expectedInput, nil
	}
}

func makeReceiveMessageInput() *sqs.ReceiveMessageInput {
	return &sqs.ReceiveMessageInput{
		QueueUrl: aws.String("queue url"),
		AttributeNames: []types.QueueAttributeName{
			"SentTimestamp",
		},
		MessageAttributeNames: []string{
			"All",
		},
		WaitTimeSeconds:     20,
		VisibilityTimeout:   10,
		MaxNumberOfMessages: 10,
	}
}

func unreachableHandler(ctx context.Context, request interface{}) (response interface{}, err error) {
	panic("n/a")
}

func nopHandler(ctx context.Context, request interface{}) (response interface{}, err error) {
	return nil, nil
}

func nopDecodeRequest(ctx context.Context, msg types.Message) (request interface{}, err error) {
	return *msg.Body, nil
}

var nopResponseHandler = []ResponseFunc{
	func(ctx context.Context, msg types.Message, response interface{}) context.Context {
		return ctx
	},
}

var panickingResponseHandler = []ResponseFunc{
	func(ctx context.Context, msg types.Message, response interface{}) context.Context {
		panic("should not be called")
	},
}

const (
	stringMessagePrefix  = "MSG-"
	msg1                 = "MSG-1"
	expectedErrorString1 = "ERR STR"
)
