package sqstransport

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/go-kit/kit/transport"
)

type (
	Client interface {
		ReceiveMessage(ctx context.Context,
			params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	}

	RequestFunc       func(context.Context, types.Message) context.Context
	DecodeRequestFunc func(context.Context, types.Message) (request interface{}, err error)
	ResponseFunc      func(ctx context.Context, msg types.Message, response interface{}) context.Context
	AfterBatchFunc    func(ctx context.Context)
	InputFactory      func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options))

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

type (
	transportErrorHandler = transport.ErrorHandler
)
