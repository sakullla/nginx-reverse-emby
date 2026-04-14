package task

type Message struct {
	Type string `json:"type"`

	Hello  *HelloMessage  `json:"hello,omitempty"`
	Task   *TaskMessage   `json:"task,omitempty"`
	Update *UpdateMessage `json:"update,omitempty"`
	Ping   *PingMessage   `json:"ping,omitempty"`
}

type HelloMessage struct {
	AgentID      string   `json:"agent_id"`
	SessionID    string   `json:"session_id"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

type TaskMessage struct {
	TaskID     string         `json:"task_id"`
	TaskType   string         `json:"task_type"`
	Deadline   string         `json:"deadline"`
	RawPayload map[string]any `json:"payload"`
}

type UpdateMessage struct {
	TaskID string `json:"task_id"`
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
}

type PingMessage struct {
	SentAt string `json:"sent_at"`
}
