package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/internal/controlplane/service"
)

type taskStreamAuthAgentService struct{ AgentService }

func (taskStreamAuthAgentService) GetByToken(context.Context, string) (service.AgentSummary, error) {
	return service.AgentSummary{ID: "edge-a"}, nil
}

type blockingTaskSession struct {
	closed  chan struct{}
	release chan struct{}
}

func (*blockingTaskSession) SendTask(service.TaskEnvelope) error { return nil }

func (session *blockingTaskSession) Close() error {
	close(session.closed)
	<-session.release
	return nil
}

type flushSignalWriter struct {
	header  http.Header
	flushed chan struct{}
	once    sync.Once
}

func (writer *flushSignalWriter) Header() http.Header { return writer.header }
func (*flushSignalWriter) Write(payload []byte) (int, error) {
	return len(payload), nil
}
func (*flushSignalWriter) WriteHeader(int) {}
func (writer *flushSignalWriter) Flush() {
	writer.once.Do(func() { close(writer.flushed) })
}

func TestTaskStreamFlushesBeforeReplacingBlockingSession(t *testing.T) {
	tasks := service.NewTaskService(service.TaskServiceConfig{})
	t.Cleanup(func() { _ = tasks.Close() })
	old := &blockingTaskSession{closed: make(chan struct{}), release: make(chan struct{})}
	if err := tasks.RegisterSession(service.TaskSessionRegistration{AgentID: "edge-a", Session: old}); err != nil {
		t.Fatal(err)
	}

	requestReader, requestWriter := io.Pipe()
	defer requestWriter.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/agents/task-stream?session_id=replacement", requestReader)
	request.ProtoMajor = 2
	request.Header.Set("X-Agent-Token", "agent-token")
	response := &flushSignalWriter{header: make(http.Header), flushed: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		Dependencies{AgentService: taskStreamAuthAgentService{}, TaskService: tasks}.handleAgentTaskStream(response, request)
		close(done)
	}()
	if _, err := io.WriteString(requestWriter, "{\"type\":\"hello\"}\n"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-response.flushed:
	case <-time.After(time.Second):
		close(old.release)
		t.Fatal("replacement response was not flushed before the old session close")
	}
	select {
	case <-old.closed:
	case <-time.After(time.Second):
		close(old.release)
		t.Fatal("superseded session close did not start")
	}
	close(old.release)
	if err := requestWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replacement stream did not stop")
	}
}
