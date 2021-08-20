package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/dc0d/sqstransport"
)

// this is a demonstrative example.
// a real world application will not be implemented like this.

// set:
// export AWS_REGION=...
// export AWS_ACCESS_KEY_ID=...
// export AWS_SECRET_ACCESS_KEY=...
// export QUEUE_NAME=...

// run:
// go run main.go

func main() {
	ctx := createRootContext()

	client := makeClient(ctx)
	queueURL := getQueueURL(ctx, client)

	sqsAdapter := &sqstransport.Subscriber{
		Handler:         handler,
		InputFactory:    inputFactory(queueURL),
		DecodeRequest:   decodeRequest,
		ResponseHandler: responseHandler(client, queueURL),
		BaseContext:     ctx,
		ErrorHandler:    errorHandlerFunc(errorHandler),
	}
	go func() { _ = sqsAdapter.Serve(client) }()

	<-ctx.Done()
}

func errorHandler(ctx context.Context, err error) {
	log.Println(err)
}

func inputFactory(queueURL string) func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) {
	return func() (params *sqs.ReceiveMessageInput, optFns []func(*sqs.Options)) {
		return &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(queueURL),
			AttributeNames: []types.QueueAttributeName{
				"SentTimestamp",
			},
			MessageAttributeNames: []string{
				"All",
			},
			WaitTimeSeconds:     20,
			VisibilityTimeout:   10,
			MaxNumberOfMessages: 10,
		}, nil
	}
}

func decodeRequest(ctx context.Context, msg types.Message) (request interface{}, err error) {
	return *msg.Body, nil
}

// handler is the port/endpoint and should be inside a separate package.
func handler(ctx context.Context, request interface{}) (response interface{}, err error) {
	log.Println(request)

	return nil, nil
}

func responseHandler(
	client responseHandlerClient,
	queueURL string) func(ctx context.Context, msg types.Message, response interface{}, err error) {
	return func(ctx context.Context, msg types.Message, response interface{}, err error) {
		if err == nil {
			log.Println("message processed successfully, deleting the message")

			input := &sqs.DeleteMessageInput{
				QueueUrl:      &queueURL,
				ReceiptHandle: aws.String(*msg.ReceiptHandle),
			}

			_, err = client.DeleteMessage(ctx, input)
			if err != nil {
				log.Println(err)
			}
		}
	}
}

//

func createRootContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())

	termSignal := make(chan os.Signal, 1)
	signal.Notify(termSignal, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-termSignal
		cancel()
	}()

	return ctx
}

func makeClient(ctx context.Context) *sqs.Client {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	return sqs.NewFromConfig(cfg)
}

func getQueueURL(ctx context.Context, client *sqs.Client) string {
	gquInput := &sqs.GetQueueUrlInput{
		QueueName: aws.String(os.Getenv("QUEUE_NAME")),
	}

	result, err := client.GetQueueUrl(ctx, gquInput)
	if err != nil {
		log.Fatal(err)
	}

	return *result.QueueUrl
}

type errorHandlerFunc func(ctx context.Context, err error)

func (fn errorHandlerFunc) Handle(ctx context.Context, err error) { fn(ctx, err) }

type responseHandlerClient interface {
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}
