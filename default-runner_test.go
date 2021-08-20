package sqstransport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var _ Runner = new(defaultRunner)

func Test_defaultRunner(t *testing.T) {
	t.Run(`should run the job`, func(t *testing.T) {
		sut := newDefaultRunner()

		done := make(chan struct{})
		sut.Run(func() { close(done) })

		assert.Eventually(t, func() bool {
			select {
			case <-done:
				return true
			default:
			}
			return false
		}, time.Millisecond*100, time.Millisecond*10)
	})

	t.Run(`should run the job concurrently`, func(t *testing.T) {
		sut := newDefaultRunner()

		done := make(chan struct{})
		sut.Run(func() { done <- struct{}{} })

		assert.Eventually(t, func() bool {
			select {
			case <-done:
				return true
			default:
			}
			return false
		}, time.Millisecond*100, time.Millisecond*10)
	})
}
