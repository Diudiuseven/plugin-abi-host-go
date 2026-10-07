package main

func ABIMetadata() string {
	return `{"schema":1,"id":"g0","version":"1","generation":0,"requires":[],"entries":[{"name":"Echo","kind":"call"}]}`
}
func Echo(s string) string { return s }
func main()                {}
