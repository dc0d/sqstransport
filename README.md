[![PkgGoDev](https://pkg.go.dev/badge/dc0d/sqstransport)](https://pkg.go.dev/github.com/dc0d/sqstransport)

# sqstransport

This package contains a go-kit transport implementation for AWS SQS.

```go
sub := &Subscriber{
    InputFactory:    ..., // create a *sqs.ReceiveMessageInput instance,
    DecodeRequest:   ..., // decode the incoming message into an endpoint request object,
    Handler: func(ctx context.Context, request interface{}) (response interface{}, err error) {
        // handle the request,
    },
    ResponseHandler: ..., // handle the response,
}

go func() { _ = sub.Serve(client) }()
```

