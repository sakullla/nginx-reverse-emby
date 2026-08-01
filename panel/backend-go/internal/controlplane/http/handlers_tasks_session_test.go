package http

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

func TestTaskSessionCloseCannotBeOverwrittenByAdmittedSendDeadline(t *testing.T) {
	tests := []struct {
		name string
		new  func(*deadlineBlockingResponseWriter, context.CancelFunc) interface {
			SendTaskContext(context.Context, service.TaskEnvelope) error
			CloseContext(context.Context) error
		}
	}{
		{
			name: "sse",
			new: func(writer *deadlineBlockingResponseWriter, cancel context.CancelFunc) interface {
				SendTaskContext(context.Context, service.TaskEnvelope) error
				CloseContext(context.Context) error
			} {
				return newSSETaskSession(writer, writer, cancel)
			},
		},
		{
			name: "ndjson",
			new: func(writer *deadlineBlockingResponseWriter, cancel context.CancelFunc) interface {
				SendTaskContext(context.Context, service.TaskEnvelope) error
				CloseContext(context.Context) error
			} {
				return newNDJSONTaskSession(writer, writer, cancel, nil)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := newDeadlineBlockingResponseWriter()
			_, cancel := context.WithCancel(context.Background())
			session := test.new(writer, cancel)
			envelope := service.TaskEnvelope{ID: "task-1", Type: service.TaskTypePKISecurityUpdate, Deadline: time.Now().Add(time.Minute), CreatedAt: time.Now()}
			sendDone := make(chan error, 1)
			go func() { sendDone <- session.SendTaskContext(t.Context(), envelope) }()
			select {
			case <-writer.futureDeadlineStarted:
			case <-time.After(time.Second):
				t.Fatal("send did not reach initial deadline admission")
			}
			closeDone := make(chan error, 1)
			go func() { closeDone <- session.CloseContext(t.Context()) }()
			close(writer.allowFutureDeadline)
			select {
			case err := <-closeDone:
				if err != nil {
					t.Fatalf("CloseContext() error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("CloseContext did not drain admitted writer")
			}
			select {
			case <-sendDone:
			case <-time.After(time.Second):
				t.Fatal("admitted send remained active after CloseContext returned")
			}
			if writer.futureDeadlineAfterImmediate() {
				t.Fatalf("future deadline overwrote close deadline: %+v", writer.deadlineSnapshot())
			}
			writes := writer.writeCount()
			if err := session.SendTaskContext(t.Context(), envelope); err == nil {
				t.Fatal("send after CloseContext unexpectedly succeeded")
			}
			if writer.writeCount() != writes {
				t.Fatal("session wrote after CloseContext returned")
			}
		})
	}
}

type deadlineBlockingResponseWriter struct {
	mu                    sync.Mutex
	header                http.Header
	deadlines             []time.Time
	futureDeadlineStarted chan struct{}
	allowFutureDeadline   chan struct{}
	writeReleased         chan struct{}
	futureOnce            sync.Once
	releaseOnce           sync.Once
	writes                int
}

func newDeadlineBlockingResponseWriter() *deadlineBlockingResponseWriter {
	return &deadlineBlockingResponseWriter{
		header: make(http.Header), futureDeadlineStarted: make(chan struct{}),
		allowFutureDeadline: make(chan struct{}), writeReleased: make(chan struct{}),
	}
}

func (w *deadlineBlockingResponseWriter) Header() http.Header { return w.header }
func (w *deadlineBlockingResponseWriter) WriteHeader(int)     {}
func (w *deadlineBlockingResponseWriter) Flush()              {}

func (w *deadlineBlockingResponseWriter) Write(value []byte) (int, error) {
	<-w.writeReleased
	w.mu.Lock()
	w.writes++
	w.mu.Unlock()
	return len(value), nil
}

func (w *deadlineBlockingResponseWriter) SetWriteDeadline(deadline time.Time) error {
	if deadline.After(time.Now()) {
		first := false
		w.futureOnce.Do(func() {
			first = true
			close(w.futureDeadlineStarted)
		})
		if first {
			<-w.allowFutureDeadline
		}
	}
	w.mu.Lock()
	w.deadlines = append(w.deadlines, deadline)
	w.mu.Unlock()
	if !deadline.After(time.Now()) {
		w.releaseOnce.Do(func() { close(w.writeReleased) })
	}
	return nil
}

func (w *deadlineBlockingResponseWriter) futureDeadlineAfterImmediate() bool {
	deadlines := w.deadlineSnapshot()
	immediateSeen := false
	for _, deadline := range deadlines {
		if !deadline.After(time.Now()) {
			immediateSeen = true
			continue
		}
		if immediateSeen {
			return true
		}
	}
	return false
}

func (w *deadlineBlockingResponseWriter) deadlineSnapshot() []time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Time(nil), w.deadlines...)
}

func (w *deadlineBlockingResponseWriter) writeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes
}
