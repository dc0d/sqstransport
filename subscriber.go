package sqstransport

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/go-kit/kit/endpoint"
	"github.com/go-kit/kit/transport"
)

// Subscriber is a go-kit sqs transport.
// Before, DecodeRequest, Handler, and ResponseHandler run inside the same anonymous function.
// This anonymous function creates a context, which uses the BaseContext as the parent,
// and is canceled when the function execution is finished.
// This anonymous function is passed to the runner.
// AfterBatch is run after each batch of messages.
type Subscriber struct {
	// message processing callbacks are required: before, decodeRequest, handler, and responseHandler
	before          []RequestFunc
	decodeRequest   DecodeRequestFunc
	handler         endpoint.Endpoint
	responseHandler []ResponseFunc

	afterBatch   AfterBatchFunc
	inputFactory InputFactory
	baseContext  func() context.Context
	runner       Runner
	errorHandler transport.ErrorHandler

	cancel context.CancelFunc

	initLock sync.Mutex
	started  bool

	onExit func()
}

// New returns a new Subscriber.
// Mandatory options start with "Use...".
// (maybe they should be explicit, might change later ¯\_(ツ)_/¯)
func New(options ...Option) *Subscriber {
	result := &Subscriber{}

	for _, opt := range options {
		opt(result)
	}

	if result.handler == nil {
		panic("Handler is required")
	}
	if result.inputFactory == nil {
		panic("InputFactory is required")
	}
	if result.decodeRequest == nil {
		panic("DecodeRequest is required")
	}
	if len(result.responseHandler) == 0 {
		panic("ResponseHandler is required")
	}

	if result.baseContext == nil {
		result.baseContext = context.Background
	}
	if result.runner == nil {
		result.runner = newDefaultRunner()
	}

	return result
}

// Serve starts receiving messages from the queue and calling the handler on each.
func (obj *Subscriber) Serve(ctx context.Context, l Client) error {
	if obj.onExit != nil {
		defer obj.onExit()
	}

	if err := obj.init(); err != nil {
		return err
	}

	ctx, obj.cancel = context.WithCancel(ctx)
	defer obj.cancel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		input, opts := obj.inputFactory()
		output, err := l.ReceiveMessage(ctx, input, opts...)
		if err != nil {
			obj.notifyError(ctx, err)
			continue
		}

		for _, msg := range output.Messages {
			obj.runHandler(obj.baseContext(), msg)
		}

		if obj.afterBatch != nil {
			obj.afterBatch(ctx)
		}
	}
}

func (obj *Subscriber) Shutdown() { obj.cancel() }

func (obj *Subscriber) runHandler(ctx context.Context, msg types.Message) {
	obj.runner.Run(func() {
		scopedCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		scopedCtx = obj.runBefore(scopedCtx, msg)

		req, err := obj.decodeRequest(scopedCtx, msg)
		if err != nil {
			err := &DecoderError{
				Err: err,
				Msg: msg,
			}
			obj.notifyError(scopedCtx, err)
			return
		}

		resp, err := obj.handler(scopedCtx, req)
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
	for _, fn := range obj.before {
		ctx = fn(ctx, msg)
		if ctx == nil {
			panic("before function returned a nil context. it must return a non-nil context")
		}
	}

	return ctx
}

func (obj *Subscriber) runResponseHandler(ctx context.Context, msg types.Message, resp interface{}) {
	for _, fn := range obj.responseHandler {
		ctx = fn(ctx, msg, resp)
		if ctx == nil {
			panic("before function returned a nil context. it must return a non-nil context")
		}
	}
}

func (obj *Subscriber) notifyError(ctx context.Context, err error) {
	if obj.errorHandler == nil {
		return
	}

	obj.errorHandler.Handle(ctx, err)
}

func (obj *Subscriber) init() error {
	obj.initLock.Lock()
	defer obj.initLock.Unlock()

	if obj.started {
		return ErrAlreadyStarted
	}

	obj.started = true

	return nil
}
