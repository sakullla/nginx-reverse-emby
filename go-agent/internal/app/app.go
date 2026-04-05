package app

type Config struct {
	AgentID string
}

type App struct {
	cfg Config
}

func New(cfg Config) (*App, error) {
	return &App{cfg: cfg}, nil
}
