package sqstransport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

var (
	_defaultRunner *defaultRunner
	_              Runner = _defaultRunner
)

func Test_defaultRunner(t *testing.T) {
	suite.Run(t, new(suiteDefaultRunner))
}

type suiteDefaultRunner struct {
	suite.Suite

	sut *defaultRunner
}

func (obj *suiteDefaultRunner) SetupTest() {
	obj.sut = newDefaultRunner()
}

func (obj *suiteDefaultRunner) Test_Run_should_run_the_job() {
	done := make(chan struct{})
	obj.sut.Run(func() { close(done) })

	obj.Eventually(func() bool {
		select {
		case <-done:
			return true
		default:
		}
		return false
	}, time.Millisecond*100, time.Millisecond*10)
}

func (obj *suiteDefaultRunner) Test_Run_should_run_the_job_concurrently() {
	done := make(chan struct{})
	obj.sut.Run(func() { done <- struct{}{} })

	obj.Eventually(func() bool {
		select {
		case <-done:
			return true
		default:
		}
		return false
	}, time.Millisecond*100, time.Millisecond*10)
}
