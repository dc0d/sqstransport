package sqstransport

type defaultRunner struct {
}

func newDefaultRunner() *defaultRunner {
	return &defaultRunner{}
}

func (obj *defaultRunner) Run(job func()) {
	go job()
}
