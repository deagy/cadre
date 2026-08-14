package production

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ShutdownManager handles graceful shutdown of the application.
type ShutdownManager struct {
	shutdown chan struct{}
	done     chan struct{}
	wg       sync.WaitGroup
	timeout  time.Duration
}

// NewShutdownManager creates a new shutdown manager.
func NewShutdownManager(timeout time.Duration) *ShutdownManager {
	return &ShutdownManager{
		shutdown: make(chan struct{}),
		done:     make(chan struct{}),
		timeout:  timeout,
	}
}

// Start begins listening for shutdown signals.
func (sm *ShutdownManager) Start() {
	sm.wg.Add(1)
	go func() {
		defer sm.wg.Done()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

		<-sigChan
		log.Println("Shutdown signal received")
		close(sm.shutdown)
	}()
}

// Wait blocks until shutdown signal is received.
func (sm *ShutdownManager) Wait() <-chan struct{} {
	return sm.shutdown
}

// Shutdown gracefully shuts down all registered handlers.
func (sm *ShutdownManager) Shutdown(handlers ...func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), sm.timeout)
	defer cancel()

	var shutdownErrors []error

	for _, handler := range handlers {
		if handler != nil {
			if err := handler(ctx); err != nil {
				shutdownErrors = append(shutdownErrors, err)
			}
		}
	}

	sm.wg.Wait()
	close(sm.done)

	if len(shutdownErrors) > 0 {
		log.Printf("Shutdown completed with %d errors", len(shutdownErrors))
		return shutdownErrors[0]
	}

	log.Println("Shutdown completed successfully")
	return nil
}

// Done returns a channel that closes when shutdown is complete.
func (sm *ShutdownManager) Done() <-chan struct{} {
	return sm.done
}

// ConnectionDrainer helps drain in-flight connections.
type ConnectionDrainer struct {
	mu        sync.Mutex
	active    int
	done      chan struct{}
	timeout   time.Duration
}

// NewConnectionDrainer creates a new connection drainer.
func NewConnectionDrainer(timeout time.Duration) *ConnectionDrainer {
	return &ConnectionDrainer{
		active:  0,
		done:    make(chan struct{}),
		timeout: timeout,
	}
}

// Acquire increments active connection count.
func (cd *ConnectionDrainer) Acquire() {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	cd.active++
}

// Release decrements active connection count.
func (cd *ConnectionDrainer) Release() {
	cd.mu.Lock()
	defer cd.mu.Unlock()

	if cd.active <= 0 {
		return // Guard against negative count and double-close panic
	}

	cd.active--
	if cd.active == 0 {
		close(cd.done)
	}
}

// Wait blocks until all connections are drained or timeout occurs.
func (cd *ConnectionDrainer) Wait(ctx context.Context) error {
	select {
	case <-cd.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ActiveCount returns the current number of active connections.
func (cd *ConnectionDrainer) ActiveCount() int {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	return cd.active
}
