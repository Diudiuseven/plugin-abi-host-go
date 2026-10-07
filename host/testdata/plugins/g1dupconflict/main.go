package main

func ABIMetadata() string {
	return `{"schema":1,"id":"g1","version":"1.0.0-conflict","generation":1,"requires":["log"],"entries":[{"name":"Echo","kind":"call"}]}`
}

// Same module identity (id g1, generation 1) as g1ok but a different
// artifact: loading it after g1ok must be rejected, not replace it.
func Echo(s string) string { return "conflict:" + s }
func main()                {}
