package config

type Config struct {
	AgentID string
}

func Default() Config {
	return Config{AgentID: "bootstrap"}
}
