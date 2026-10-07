package main

func ABIMetadata() string {
	return `{"schema":1,"id":"g1dup","version":"1","generation":1,"requires":[],"entries":[{"name":"Echo","kind":"call"},{"name":"Echo","kind":"call"}]}`
}
func Echo(s string) string { return s }
func main()                {}
