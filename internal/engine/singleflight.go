package engine

import (
	"context"
	"sync"
)

type flightCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

type flightGroup[T any] struct {
	mu sync.Mutex
	m  map[string]*flightCall[T]
}

// Acquire joins an in-flight call or reserves leadership for key. A leader must
// call complete exactly once. Followers wait with their own context, so a
// cancelled request never blocks on another request indefinitely.
func (g *flightGroup[T]) Acquire(key string) (leader bool, complete func(T, error), wait func(context.Context) (T, error)) {
	g.mu.Lock()
	if g.m == nil {
		g.m = map[string]*flightCall[T]{}
	}
	if existing := g.m[key]; existing != nil {
		g.mu.Unlock()
		return false, nil, func(ctx context.Context) (T, error) {
			select {
			case <-existing.done:
				return existing.val, existing.err
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			}
		}
	}
	call := &flightCall[T]{done: make(chan struct{})}
	g.m[key] = call
	g.mu.Unlock()
	var once sync.Once
	finish := func(value T, err error) {
		once.Do(func() {
			call.val, call.err = value, err
			close(call.done)
			g.mu.Lock()
			delete(g.m, key)
			g.mu.Unlock()
		})
	}
	waitFn := func(ctx context.Context) (T, error) {
		select {
		case <-call.done:
			return call.val, call.err
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
	return true, finish, waitFn
}

func (g *flightGroup[T]) Do(key string, fn func() (T, error)) (T, error) {
	leader, complete, wait := g.Acquire(key)
	if !leader {
		return wait(context.Background())
	}
	value, err := fn()
	complete(value, err)
	return value, err
}
