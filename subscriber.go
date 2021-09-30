package sqstransport

import (
	"context"
	"errors"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/go-kit/kit/endpoint"
	"github.com/go-kit/kit/transport"
)

// Subscriber is a go-kit sqs transport.
type Subscriber struct {
	// Before is optional. Can be used for starting a keep-in-flight hearbeat - an example.
	// They run before DecodeRequest and can put additional data inside the context.
	// If returns a nil context, it causes a panic.
	Before []RequestFunc

	// DecodeRequest is required.
	DecodeRequest DecodeRequestFunc

	// Handler is required.
	Handler endpoint.Endpoint

	// ResponseHandler is required. Any actions required after executing handler can take place here.
	// Like deleting the message after being successfully processed.
	ResponseHandler []ResponseFunc

	// AfterBatch is optional. It is called after a batch of messages passed to the Runner.
	AfterBatch AfterBatchFunc

	// InputFactory is required.
	// It must return a non-nil params.
	// It can return nil for optFns.
	InputFactory func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options))

	// BaseContext if not provided, will be context.Background().
	BaseContext context.Context

	// Runner if not provided, the default runner will be used.
	// All the Befor functions, decoding the message, handling the message
	// and handling the response are executed by the Runner.
	Runner Runner

	// ErrorHandler is optional.
	ErrorHandler transport.ErrorHandler

	cancel context.CancelFunc

	initLock sync.Mutex
	started  bool

	onExit func()
}

// Serve starts receiving messages from the queue and calling the handler on each.
// It blocks until the BaseContext is cenceled or Shutdown is called.
func (obj *Subscriber) Serve(l Client) error {
	if obj.onExit != nil {
		defer obj.onExit()
	}

	if err := obj.init(); err != nil {
		return err
	}

	var ctx context.Context
	ctx, obj.cancel = context.WithCancel(obj.BaseContext)
	defer obj.cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		input, opts := obj.InputFactory()
		output, err := l.ReceiveMessage(ctx, input, opts...)
		if err != nil {
			obj.notifyError(ctx, err)
			continue
		}

		for _, msg := range output.Messages {
			obj.runHandler(ctx, msg)
		}

		if obj.AfterBatch != nil {
			obj.AfterBatch(ctx)
		}
	}
}

func (obj *Subscriber) Shutdown() { obj.cancel() }

func (obj *Subscriber) runHandler(ctx context.Context, msg types.Message) {
	obj.Runner.Run(func() {
		scopedCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		scopedCtx = obj.runBefore(scopedCtx, msg)

		req, err := obj.DecodeRequest(scopedCtx, msg)
		if err != nil {
			err := &DecoderError{
				Err: err,
				Msg: msg,
			}
			obj.notifyError(scopedCtx, err)
			return
		}

		resp, err := obj.Handler(scopedCtx, req)
		if err != nil {
			err := &HandlerError{
				Err:     err,
				Request: req,
				Msg:     msg,
			}
			obj.notifyError(scopedCtx, err)
			return
		}

		obj.runResponseHandler(scopedCtx, msg, resp)
	})
}

func (obj *Subscriber) runBefore(ctx context.Context, msg types.Message) context.Context {
	for _, fn := range obj.Before {
		ctx = fn(ctx, msg)
		if ctx == nil {
			panic("before function returned a nil context. it must return a non-nil context")
		}
	}

	return ctx
}

func (obj *Subscriber) runResponseHandler(ctx context.Context, msg types.Message, resp interface{}) {
	for _, fn := range obj.ResponseHandler {
		ctx = fn(ctx, msg, resp)
		if ctx == nil {
			panic("before function returned a nil context. it must return a non-nil context")
		}
	}
}

func (obj *Subscriber) notifyError(ctx context.Context, err error) {
	if obj.ErrorHandler == nil {
		return
	}

	obj.ErrorHandler.Handle(ctx, err)
}

func (obj *Subscriber) init() error {
	obj.initLock.Lock()
	defer obj.initLock.Unlock()

	if obj.started {
		return ErrAlreadyStarted
	}

	if obj.Handler == nil {
		panic("Handler is required")
	}
	if obj.InputFactory == nil {
		panic("InputFactory is required")
	}
	if obj.DecodeRequest == nil {
		panic("DecodeRequest is required")
	}
	if len(obj.ResponseHandler) == 0 {
		panic("ResponseHandler is required")
	}

	if obj.BaseContext == nil {
		obj.BaseContext = context.Background()
	}
	if obj.Runner == nil {
		obj.Runner = newDefaultRunner()
	}

	obj.started = true

	return nil
}

type (
	Client interface {
		ReceiveMessage(ctx context.Context,
			params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	}

	RequestFunc       func(context.Context, types.Message) context.Context
	DecodeRequestFunc func(context.Context, types.Message) (request interface{}, err error)
	ResponseFunc      func(ctx context.Context, msg types.Message, response interface{}) context.Context
	AfterBatchFunc    func(ctx context.Context)

	Runner interface {
		Run(func())
	}
)

// HandlerError is used for triggering the error handler when the handler returns an error.
type HandlerError struct {
	Err     error
	Request interface{}
	Msg     types.Message
}

func (obj *HandlerError) Error() string {
	if obj.Err == nil {
		return defaultHandlerErrorMsh
	}

	return obj.Err.Error()
}

type DecoderError struct {
	Err error
	Msg types.Message
}

func (obj *DecoderError) Error() string {
	if obj.Err == nil {
		return defaultDecoderErrorMsh
	}

	return obj.Err.Error()
}

const (
	defaultHandlerErrorMsh = "HandlerError"
	defaultDecoderErrorMsh = "DecoderError"
)

var (
	ErrAlreadyStarted = errors.New("already started")
)
