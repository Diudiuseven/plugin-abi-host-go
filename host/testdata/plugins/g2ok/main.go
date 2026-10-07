package main

// ABIMetadata is the version-neutral manifest entry.
func ABIMetadata() string {
	return `{"schema":1,"id":"g2","version":"2.0.0","generation":2,"requires":["log","metrics"],"entries":[{"name":"Process","kind":"call"}]}`
}

// Process is a generation 2 entry: func(map[string]any)(map[string]any, error).
func Process(in map[string]any) (map[string]any, error) {
	s, _ := in["x"].(string)
	return map[string]any{"echo": s, "gen": 2}, nil
}

func main() {}
