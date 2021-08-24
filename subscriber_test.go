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
)

func Test_Subscriber_should_stop_when_shutdown_is_called(t *testing.T) {
	t.Parallel()

	stopped := make(chan struct{})
	sut := &Subscriber{
		Handler:         nopHandler,
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		onExit:          func() { close(stopped) },
	}

	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		_ = sut.Serve(mockClient10msec())
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

func Test_Subscriber_should_stop_when_base_context_is_canceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan struct{})
	sut := &Subscriber{
		Handler:         nopHandler,
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
		onExit:          func() { close(stopped) },
	}

	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		_ = sut.Serve(mockClient10msec())
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

func Test_Subscriber_should_error_if_called_more_than_once(t *testing.T) {
	t.Parallel()

	client := mockClient10msec()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sut := &Subscriber{
		Handler:         nopHandler,
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
	}

	expectedError := ErrAlreadyStarted

	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		_ = sut.Serve(client)
	}()
	<-serverStarted

	actualError := sut.Serve(client)
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

	sut := &Subscriber{
		Handler: func(ctx context.Context, request interface{}) (response interface{}, err error) {
			actualRequestLock.Lock()
			defer actualRequestLock.Unlock()
			actualRequest = request
			return nil, nil
		},
		InputFactory:    inputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
	}

	go func() { _ = sut.Serve(client) }()

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

	sut := &Subscriber{
		Handler: func(ctx context.Context, request interface{}) (response interface{}, err error) {
			incomingLock.Lock()
			defer incomingLock.Unlock()
			incoming = append(incoming, fmt.Sprint(request))
			return nil, nil
		},
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
	}

	go func() { _ = sut.Serve(mockClient10msec()) }()

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

func Test_Subscriber_should_call_the_ResponseHandler_after_handler(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const expectedResponse = "a response"

	actualResponseLock := &sync.Mutex{}
	var actualResponse interface{}

	sut := &Subscriber{
		Handler: func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return expectedResponse, nil
		},
		InputFactory:  defaultInputFactory,
		DecodeRequest: nopDecodeRequest,
		ResponseHandler: func(ctx context.Context, msg types.Message, response interface{}, err error) {
			actualResponseLock.Lock()
			defer actualResponseLock.Unlock()
			actualResponse = response
		},
		BaseContext: ctx,
	}

	go func() { _ = sut.Serve(mockClient10msec()) }()

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

	sut := &Subscriber{
		Handler:         unreachableHandler,
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
		ErrorHandler:    errorHandler,
	}

	go func() { _ = sut.Serve(client) }()

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

	sut := &Subscriber{
		Handler:         unreachableHandler,
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
	}

	go func() { _ = sut.Serve(client) }()

	assert.Eventually(t, func() bool {
		return len(client.ReceiveMessageCalls()) > 3
	}, time.Millisecond*300, time.Millisecond*20)
}

func Test_Subscriber_should_call_the_error_handler_on_returned_error_from_decode_request(t *testing.T) {
	t.Parallel()

	expectedError := errors.New("an error")

	decodeRequest := func(ctx context.Context, msg types.Message) (request interface{}, err error) {
		return nil, expectedError
	}

	errorHandler := &TransportErrorHandlerSpy{
		HandleFunc: func(ctx context.Context, err error) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sut := &Subscriber{
		Handler: func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return nil, nil
		},
		InputFactory:    defaultInputFactory,
		DecodeRequest:   decodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
		ErrorHandler:    errorHandler,
	}

	go func() { _ = sut.Serve(mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		if len(errorHandler.HandleCalls()) == 0 {
			return false
		}

		return errorHandler.HandleCalls()[0].Err == expectedError
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

	sut := &Subscriber{
		Handler: func(ctx context.Context, request interface{}) (response interface{}, err error) {
			handlerCtxLock.Lock()
			defer handlerCtxLock.Unlock()
			handlerCtx = ctx

			return nil, nil
		},
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
		Before: []RequestFunc{
			func(ctx context.Context, msg types.Message) context.Context {
				return context.WithValue(ctx, counterKey, 1)
			},
			func(ctx context.Context, msg types.Message) context.Context {
				previous := ctx.Value(counterKey).(int)
				return context.WithValue(ctx, counterKey, previous+1)
			},
		},
	}

	go func() { _ = sut.Serve(mockClient10msec()) }()

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

	sut := &Subscriber{
		Handler: func(ctx context.Context, request interface{}) (response interface{}, err error) {
			return nil, nil
		},
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
		AfterBatch: func(ctx context.Context) {
			atomic.AddInt64(&afterBatchCalls, 1)
		},
	}

	go func() { _ = sut.Serve(mockClient10msec()) }()

	assert.Eventually(t, func() bool {
		return atomic.LoadInt64(&afterBatchCalls) > 3
	}, time.Millisecond*100, time.Millisecond*20)
}

func Test_Subscriber_should_panic_if_any_before_function_returns_a_nil_context(t *testing.T) {
	sut := &Subscriber{
		Before: []RequestFunc{
			func(ctx context.Context, msg types.Message) context.Context {
				return nil
			},
		},
	}

	assert.Panics(t, func() { sut.runBefore(context.Background(), types.Message{}) })
}

func Test_Subscriber_init(t *testing.T) {
	t.Parallel()

	t.Run(`should panic if Handler is nil`, func(t *testing.T) {
		sut := &Subscriber{}

		assert.PanicsWithValue(t, "Handler is required", func() {
			_ = sut.init()
		})
	})

	t.Run(`should panic if InputFactory is nil`, func(t *testing.T) {
		sut := &Subscriber{
			Handler: unreachableHandler,
		}

		assert.PanicsWithValue(t, "InputFactory is required", func() {
			_ = sut.init()
		})
	})

	t.Run(`should panic if DecodeRequest is nil`, func(t *testing.T) {
		sut := &Subscriber{
			Handler:      unreachableHandler,
			InputFactory: func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) { panic("n/a") },
		}

		assert.PanicsWithValue(t, "DecodeRequest is required", func() {
			_ = sut.init()
		})
	})

	t.Run(`should panic if ResponseHandler is nil`, func(t *testing.T) {
		sut := &Subscriber{
			Handler:       unreachableHandler,
			InputFactory:  func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) { panic("n/a") },
			DecodeRequest: func(context.Context, types.Message) (request interface{}, err error) { panic("n/a") },
		}

		assert.PanicsWithValue(t, "ResponseHandler is required", func() {
			_ = sut.init()
		})
	})

	t.Run(`should use context.Background() if BaseContext is not provided`, func(t *testing.T) {
		sut := &Subscriber{
			Handler:         unreachableHandler,
			InputFactory:    func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) { panic("n/a") },
			DecodeRequest:   func(context.Context, types.Message) (request interface{}, err error) { panic("n/a") },
			ResponseHandler: func(ctx context.Context, msg types.Message, response interface{}, err error) { panic("n/a") },
		}

		_ = sut.init()

		assert.Equal(t, context.Background(), sut.BaseContext)
	})

	t.Run(`should use default runner if Runner is not provided`, func(t *testing.T) {
		sut := &Subscriber{
			Handler:         unreachableHandler,
			InputFactory:    func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) { panic("n/a") },
			DecodeRequest:   func(context.Context, types.Message) (request interface{}, err error) { panic("n/a") },
			ResponseHandler: func(ctx context.Context, msg types.Message, response interface{}, err error) { panic("n/a") },
		}

		_ = sut.init()

		assert.IsType(t, &defaultRunner{}, sut.Runner)
	})

	t.Run(`should error if called more than once`, func(t *testing.T) {
		sut := &Subscriber{
			Handler:         unreachableHandler,
			InputFactory:    func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) { panic("n/a") },
			DecodeRequest:   func(context.Context, types.Message) (request interface{}, err error) { panic("n/a") },
			ResponseHandler: func(ctx context.Context, msg types.Message, response interface{}, err error) { panic("n/a") },
		}

		expectedError := ErrAlreadyStarted

		actualError := sut.init()
		assert.NoError(t, actualError)

		actualError = sut.init()
		assert.Equal(t, expectedError, actualError)
	})
}

func ExampleSubscriber() {
	client := mockClient10msec()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handled := make(chan struct{})
	sut := &Subscriber{
		Handler: func(ctx context.Context, request interface{}) (response interface{}, err error) {
			select {
			case <-handled:
				return nil, nil
			default:
			}

			defer close(handled)

			// processing the request inside the endpoint
			fmt.Println(request)

			return nil, nil
		},
		InputFactory:    defaultInputFactory,
		DecodeRequest:   nopDecodeRequest,
		ResponseHandler: nopResponseHandler,
		BaseContext:     ctx,
	}

	go func() { _ = sut.Serve(client) }()

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

func nopResponseHandler(ctx context.Context, msg types.Message, response interface{}, err error) {}

const (
	stringMessagePrefix = "MSG-"
)
