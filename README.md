[![PkgGoDev](https://pkg.go.dev/badge/dc0d/sqstransport)](https://pkg.go.dev/github.com/dc0d/sqstransport)

# sqstransport

This package contains a go-kit transport implementation for AWS SQS.

```go
sub := New(
    WithBefore(...),
    WithBefore(...),
    UseHandler(...),        // handle the request,
    UseDecodeRequest(...),  // decode the incoming message into an endpoint request object,
    UseResponseHandler(...),
    UseResponseHandler(...),
    UseInputFactory(...),   // create a *sqs.ReceiveMessageInput instance,
    WithBaseContext(...),   // used for processing each new message
    WithErrorHandler(...),
)

go func() { _ = sub.Serve(client) }()
```

