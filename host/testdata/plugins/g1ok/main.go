package main

// ABIMetadata is the version-neutral manifest entry.
func ABIMetadata() string {
	return `{"schema":1,"id":"g1","version":"1.0.0","generation":1,"requires":["log"],"entries":[{"name":"Echo","kind":"call"}]}`
}

// Echo is a generation 1 entry: func(string) string.
func Echo(s string) string { return "g1:" + s }

func main() {}
