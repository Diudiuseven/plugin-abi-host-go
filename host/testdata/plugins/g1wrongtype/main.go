package main

func ABIMetadata() string {
	return `{"schema":1,"id":"g1wrongtype","version":"1","generation":1,"requires":[],"entries":[{"name":"Echo","kind":"call"}]}`
}

// Echo exists but has the wrong signature for a generation 1 entry.
func Echo(s string) (string, error) { return s, nil }

func main() {}
