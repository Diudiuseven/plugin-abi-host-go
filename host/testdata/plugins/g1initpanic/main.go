package main

func init() {
	panic("boom during package init")
}

func ABIMetadata() string {
	return `{"schema":1,"id":"initpanic","version":"1","generation":1,"requires":[],"entries":[{"name":"Echo","kind":"call"}]}`
}
func Echo(s string) string { return s }
func main() {}
