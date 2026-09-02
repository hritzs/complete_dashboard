package trading

import (
	"os"
	"path/filepath"
	"strings"
)

func LoadEnv() {
	envPath := filepath.Join("..", "..", ".env")

	data, err := os.ReadFile(envPath)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"`)

		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}
